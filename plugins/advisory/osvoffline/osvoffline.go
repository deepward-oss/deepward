// Package osvoffline is the offline OSV advisory source: it reads the
// per-ecosystem OSV exports (all.zip) from a local directory, refreshed with
// `deepward db update`.
package osvoffline

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/osvschema"
)

// ID is the plugin ID.
const ID = "osv-offline"

// DefaultBaseURL hosts the OSV exports.
const DefaultBaseURL = "https://osv-vulnerabilities.storage.googleapis.com"

// Config is plugins.osv-offline.
type Config struct {
	// Dir holds <Ecosystem>/all.zip (default: the plugin cache directory).
	Dir        string   `yaml:"dir" json:"dir,omitempty"`
	BaseURL    string   `yaml:"baseUrl" json:"baseUrl"`
	Ecosystems []string `yaml:"ecosystems" json:"ecosystems"`
}

type metadata struct {
	ETag       string    `json:"etag"`
	Updated    time.Time `json:"updated"`
	Advisories int       `json:"advisories"`
}

// Source reads local OSV exports.
type Source struct {
	cfg   Config
	dir   string
	HTTP  *http.Client
	clock sdk.Clock
	once  sync.Once
	index map[deps.Ecosystem]map[string][]deps.Advisory
	err   error
}

func init() { sdk.Register(New()) }

// New returns the source.
func New() *Source {
	return &Source{cfg: Config{BaseURL: DefaultBaseURL, Ecosystems: []string{"Maven", "NuGet", "npm", "Go", "crates.io"}}}
}

// Info implements sdk.Plugin.
func (s *Source) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: deps.KindAdvisorySource, Version: sdk.BuiltinVersion,
		Description: "Local copy of the OSV database for offline scans (deepward db update)"}
}

// ConfigTarget implements sdk.Configurable.
func (s *Source) ConfigTarget() any { return &s.cfg }

// Init implements sdk.Initializer.
func (s *Source) Init(_ context.Context, h sdk.Host) error {
	s.dir = s.cfg.Dir
	if s.dir == "" {
		s.dir = h.Settings().CacheDir
	}
	s.clock = h.Clock()
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	return nil
}

// DataLicense implements deps.AdvisorySource.
func (s *Source) DataLicense() deps.DataLicense {
	return deps.DataLicense{SPDX: "CC-BY-4.0", Attribution: "OSV.dev exports and their upstream databases (GitHub Advisory Database, CC-BY-4.0)",
		URL: "https://google.github.io/osv.dev/data/"}
}

func (s *Source) zipPath(eco string) string  { return filepath.Join(s.dir, eco, "all.zip") }
func (s *Source) metaPath(eco string) string { return filepath.Join(s.dir, eco, "metadata.json") }

// Available implements deps.OfflineSource.
func (s *Source) Available() bool {
	if s.dir == "" {
		return false
	}
	for _, eco := range s.cfg.Ecosystems {
		if _, err := os.Stat(s.zipPath(eco)); err != nil {
			return false
		}
	}
	return len(s.cfg.Ecosystems) > 0
}

// Freshness implements deps.AdvisorySource: the oldest export update time.
func (s *Source) Freshness() *time.Time {
	var oldest *time.Time
	for _, eco := range s.cfg.Ecosystems {
		m, err := s.readMeta(eco)
		if err != nil {
			continue
		}
		t := m.Updated
		if oldest == nil || t.Before(*oldest) {
			oldest = &t
		}
	}
	return oldest
}

func (s *Source) readMeta(eco string) (metadata, error) {
	var m metadata
	b, err := os.ReadFile(s.metaPath(eco))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func (s *Source) load() error {
	s.once.Do(func() {
		s.index = map[deps.Ecosystem]map[string][]deps.Advisory{}
		for _, ecoName := range s.cfg.Ecosystems {
			eco, ok := osvschema.Ecosystem(ecoName)
			if !ok {
				continue
			}
			idx, err := readExport(s.zipPath(ecoName))
			if err != nil {
				s.err = fmt.Errorf("offline database %s: %w (run `deepward db update`)", ecoName, err)
				return
			}
			s.index[eco] = idx
		}
	})
	return s.err
}

func readExport(path string) (map[string][]deps.Advisory, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	idx := map[string][]deps.Advisory{}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		v, err := osvschema.Decode(data)
		if err != nil || v.IsWithdrawn() {
			continue
		}
		adv := v.ToAdvisory(ID)
		for _, a := range adv.Affected {
			key := strings.ToLower(a.Name)
			idx[key] = append(idx[key], adv)
		}
	}
	return idx, nil
}

// Query implements deps.AdvisorySource; it never accesses the network.
func (s *Source) Query(ctx context.Context, queries []deps.PackageQuery) ([]deps.Advisory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(queries) == 0 {
		return nil, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []deps.Advisory
	for _, q := range queries {
		for _, adv := range s.index[q.Ecosystem][strings.ToLower(q.Name)] {
			if !seen[adv.ID] {
				seen[adv.ID] = true
				out = append(out, adv)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpdateData implements sdk.DataUpdater: it downloads each export when its
// ETag changed.
func (s *Source) UpdateData(ctx context.Context) (string, error) {
	if s.dir == "" {
		return "", fmt.Errorf("no directory for the offline database (set plugins.%s.dir)", ID)
	}
	var parts []string
	for _, eco := range s.cfg.Ecosystems {
		msg, err := s.update(ctx, eco)
		if err != nil {
			return "", err
		}
		parts = append(parts, eco+": "+msg)
	}
	return strings.Join(parts, "; "), nil
}

func (s *Source) update(ctx context.Context, eco string) (string, error) {
	url := strings.TrimSuffix(s.cfg.BaseURL, "/") + "/" + eco + "/all.zip"
	old, _ := s.readMeta(eco)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(s.zipPath(eco)); statErr == nil && old.ETag != "" {
		req.Header.Set("If-None-Match", old.ETag)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return "up to date", nil
	case http.StatusOK:
	default:
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(s.zipPath(eco)), 0o755); err != nil {
		return "", err
	}
	tmp := s.zipPath(eco) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	idx, err := readExport(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("downloaded %s is not a valid export: %w", url, err)
	}
	if err := os.Rename(tmp, s.zipPath(eco)); err != nil {
		return "", err
	}
	count := 0
	ids := map[string]bool{}
	for _, advs := range idx {
		for _, a := range advs {
			if !ids[a.ID] {
				ids[a.ID] = true
				count++
			}
		}
	}
	now := time.Now().UTC()
	if s.clock != nil {
		now = s.clock.Now()
	}
	b, _ := json.Marshal(metadata{ETag: resp.Header.Get("ETag"), Updated: now, Advisories: count})
	if err := os.WriteFile(s.metaPath(eco), b, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("downloaded %d advisories", count), nil
}
