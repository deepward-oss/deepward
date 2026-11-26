// Package ghsa queries the GitHub Advisory Database (global security
// advisories REST API). It is disabled by default because OSV already
// includes GHSA; enable it to cross-check and fill CVSS scores.
package ghsa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// ID is the plugin ID.
const ID = "ghsa"

// Config is plugins.ghsa.
type Config struct {
	APIURL   string `yaml:"apiUrl" json:"apiUrl"`
	TokenEnv string `yaml:"tokenEnv" json:"tokenEnv"`
	// ChunkSize is the number of packages per request (max 100).
	ChunkSize int `yaml:"chunkSize" json:"chunkSize"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	if c.ChunkSize < 0 || c.ChunkSize > 100 {
		return errors.New("chunkSize must be between 1 and 100")
	}
	return nil
}

// Source is the GitHub Advisory Database.
type Source struct {
	cfg    Config
	HTTP   *http.Client
	Getenv func(string) string
}

func init() { sdk.Register(New()) }

// New returns the source.
func New() *Source {
	return &Source{cfg: Config{APIURL: "https://api.github.com", TokenEnv: "GITHUB_TOKEN", ChunkSize: 50}}
}

// Info implements sdk.Plugin.
func (s *Source) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: deps.KindAdvisorySource, Version: sdk.BuiltinVersion, DisabledByDefault: true,
		Description: "GitHub Advisory Database REST API (optional cross-check; a token raises the rate limit)"}
}

// ConfigTarget implements sdk.Configurable.
func (s *Source) ConfigTarget() any { return &s.cfg }

// DataLicense implements deps.AdvisorySource.
func (s *Source) DataLicense() deps.DataLicense {
	return deps.DataLicense{SPDX: "CC-BY-4.0", Attribution: "GitHub Advisory Database",
		URL: "https://github.com/github/advisory-database/blob/main/LICENSE.md"}
}

// Freshness implements deps.AdvisorySource.
func (s *Source) Freshness() *time.Time { return nil }

type apiAdvisory struct {
	GHSAID      string     `json:"ghsa_id"`
	CVEID       string     `json:"cve_id"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Severity    string     `json:"severity"`
	HTMLURL     string     `json:"html_url"`
	PublishedAt time.Time  `json:"published_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	WithdrawnAt *time.Time `json:"withdrawn_at"`
	References  []string   `json:"references"`
	Identifiers []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"identifiers"`
	CVSSSeverities struct {
		V3 *struct {
			Vector string  `json:"vector_string"`
			Score  float64 `json:"score"`
		} `json:"cvss_v3"`
		V4 *struct {
			Vector string  `json:"vector_string"`
			Score  float64 `json:"score"`
		} `json:"cvss_v4"`
	} `json:"cvss_severities"`
	Vulnerabilities []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		FirstPatchedVersion    string `json:"first_patched_version"`
	} `json:"vulnerabilities"`
}

// ghsaEcosystems maps GitHub's ecosystem names to deepward's.
var ghsaEcosystems = map[string]deps.Ecosystem{"maven": deps.Maven, "nuget": deps.NuGet, "npm": deps.NPM, "go": deps.Go, "rust": deps.CratesIO}

func ecosystemParam(e deps.Ecosystem) string {
	for name, eco := range ghsaEcosystems {
		if eco == e {
			return name
		}
	}
	return strings.ToLower(string(e))
}

// Query implements deps.AdvisorySource.
func (s *Source) Query(ctx context.Context, queries []deps.PackageQuery) ([]deps.Advisory, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	byEco := map[deps.Ecosystem][]string{}
	for _, q := range queries {
		byEco[q.Ecosystem] = append(byEco[q.Ecosystem], q.Name+"@"+deps.AdvisoryVersion(q.Ecosystem, q.Version))
	}
	ecos := make([]string, 0, len(byEco))
	for e := range byEco {
		ecos = append(ecos, string(e))
	}
	sort.Strings(ecos)
	size := s.cfg.ChunkSize
	if size <= 0 {
		size = 50
	}
	seen := map[string]bool{}
	var out []deps.Advisory
	for _, e := range ecos {
		pkgs := byEco[deps.Ecosystem(e)]
		for len(pkgs) > 0 {
			n := min(size, len(pkgs))
			chunk := pkgs[:n]
			pkgs = pkgs[n:]
			q := url.Values{"ecosystem": {ecosystemParam(deps.Ecosystem(e))}, "affects": {strings.Join(chunk, ",")}, "per_page": {"100"}}
			next := strings.TrimSuffix(s.cfg.APIURL, "/") + "/advisories?" + q.Encode()
			for next != "" {
				advs, link, err := s.page(ctx, next)
				if err != nil {
					return nil, err
				}
				for _, a := range advs {
					if a.WithdrawnAt != nil || seen[a.GHSAID] {
						continue
					}
					seen[a.GHSAID] = true
					out = append(out, convert(a))
				}
				next = link
			}
		}
	}
	return out, nil
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (s *Source) page(ctx context.Context, u string) ([]apiAdvisory, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "deepward")
	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if tok := getenv(s.cfg.TokenEnv); tok != "" && s.cfg.TokenEnv != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("ghsa: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("ghsa: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(200, len(data))])))
	}
	var advs []apiAdvisory
	if err := json.Unmarshal(data, &advs); err != nil {
		return nil, "", fmt.Errorf("ghsa: invalid response: %w", err)
	}
	link := ""
	if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
		link = m[1]
	}
	return advs, link, nil
}

func convert(a apiAdvisory) deps.Advisory {
	adv := deps.Advisory{ID: a.GHSAID, Summary: a.Summary, Details: a.Description, DatabaseSeverity: a.Severity,
		Published: a.PublishedAt, Modified: a.UpdatedAt, Source: ID}
	for _, id := range a.Identifiers {
		if id.Value != "" && id.Value != a.GHSAID {
			adv.Aliases = append(adv.Aliases, id.Value)
		}
	}
	if a.CVEID != "" && !contains(adv.Aliases, a.CVEID) {
		adv.Aliases = append(adv.Aliases, a.CVEID)
	}
	if v := a.CVSSSeverities.V3; v != nil && v.Vector != "" {
		adv.Severity = append(adv.Severity, deps.Score{Type: "CVSS_V3", Score: v.Vector, Value: v.Score})
	}
	if v := a.CVSSSeverities.V4; v != nil && v.Vector != "" {
		adv.Severity = append(adv.Severity, deps.Score{Type: "CVSS_V4", Score: v.Vector, Value: v.Score})
	}
	for _, v := range a.Vulnerabilities {
		eco, ok := ghsaEcosystems[strings.ToLower(v.Package.Ecosystem)]
		if !ok {
			continue
		}
		af := deps.Affected{Ecosystem: eco, Name: v.Package.Name}
		events, exact := RangeToEvents(v.VulnerableVersionRange)
		if exact != "" {
			af.Versions = []string{exact}
		} else {
			af.Ranges = []deps.Range{{Type: "ECOSYSTEM", Events: events}}
		}
		adv.Affected = append(adv.Affected, af)
	}
	if a.HTMLURL != "" {
		adv.References = append(adv.References, sdk.Reference{Type: "ADVISORY", URL: a.HTMLURL})
	}
	for _, r := range a.References {
		adv.References = append(adv.References, sdk.Reference{Type: "WEB", URL: r})
	}
	return adv
}

// RangeToEvents converts a GitHub vulnerable_version_range such as
// ">= 2.0-beta9, < 2.15.0" into OSV events. A single "= x" returns the exact
// version instead.
func RangeToEvents(r string) ([]deps.Event, string) {
	var events []deps.Event
	lower := false
	for _, part := range strings.Split(r, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, ">="):
			events = append(events, deps.Event{Introduced: strings.TrimSpace(part[2:])})
			lower = true
		case strings.HasPrefix(part, ">"):
			events = append(events, deps.Event{Introduced: strings.TrimSpace(part[1:])})
			lower = true
		case strings.HasPrefix(part, "<="):
			events = append(events, deps.Event{LastAffected: strings.TrimSpace(part[2:])})
		case strings.HasPrefix(part, "<"):
			events = append(events, deps.Event{Fixed: strings.TrimSpace(part[1:])})
		case strings.HasPrefix(part, "="):
			return nil, strings.TrimSpace(part[1:])
		}
	}
	if !lower {
		events = append([]deps.Event{{Introduced: "0"}}, events...)
	}
	return events, ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
