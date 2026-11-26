// Package outdated is the outdated-dependencies analyzer: it reports direct
// dependencies with newer published versions and proposes updates.
package outdated

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/analyzer/outdated/domain"
)

// ID is the plugin ID.
const ID = "outdated-dependencies"

// Config is plugins.outdated-dependencies.
type Config struct {
	Rules         []domain.Rule `yaml:"rules" json:"rules"`
	ExcludeScopes []string      `yaml:"excludeScopes" json:"excludeScopes"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, r := range c.Rules {
		if r.Match == "" {
			return fmt.Errorf("rules[%d].match is required", i)
		}
		switch r.MaxUpdate {
		case deps.UpdateNone, deps.UpdatePatch, deps.UpdateMinor, deps.UpdateMajor:
		default:
			return fmt.Errorf("rules[%d].maxUpdate must be patch, minor or major", i)
		}
		if r.Allow != "" {
			if _, err := domain.ParseRanges(r.Allow); err != nil {
				return fmt.Errorf("rules[%d].allow: %w", i, err)
			}
		}
	}
	return nil
}

// Analyzer reports outdated direct dependencies.
type Analyzer struct {
	cfg  Config
	host sdk.Host
	mu   sync.Mutex
	warn []sdk.Warning
}

func init() { sdk.Register(New()) }

// New returns the analyzer.
func New() *Analyzer { return &Analyzer{} }

// Info implements sdk.Plugin.
func (a *Analyzer) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindAnalyzer, Version: sdk.BuiltinVersion,
		Description: "Reports direct dependencies with newer versions (patch, minor, major) and proposes updates"}
}

// ConfigTarget implements sdk.Configurable.
func (a *Analyzer) ConfigTarget() any { return &a.cfg }

// Init implements sdk.Initializer.
func (a *Analyzer) Init(_ context.Context, h sdk.Host) error { a.host = h; return nil }

// Scope implements sdk.Analyzer.
func (a *Analyzer) Scope() sdk.AnalyzerScope { return sdk.ScopeProject }

// Requires implements sdk.Analyzer.
func (a *Analyzer) Requires() []string { return []string{deps.FactDependencyGraph} }

// Warnings implements sdk.WarningReporter.
func (a *Analyzer) Warnings() []sdk.Warning {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sdk.Warning{}, a.warn...)
}

func (a *Analyzer) registry(ecosystem string) deps.VersionRegistry {
	if a.host == nil {
		return nil
	}
	for _, p := range a.host.Plugins(sdk.KindEcosystem) {
		if p.Info().ID == ecosystem {
			if r, ok := p.(deps.VersionRegistry); ok {
				return r
			}
		}
	}
	return nil
}

// Analyze implements sdk.Analyzer.
func (a *Analyzer) Analyze(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
	if req.Project == nil {
		return nil, nil
	}
	graph, err := sdk.GetFact(ctx, req.Facts, deps.GraphKey)
	if err != nil {
		return nil, err
	}
	registry := a.registry(req.Project.Ecosystem)
	if registry == nil {
		return nil, nil
	}
	excluded := map[string]bool{}
	for _, s := range a.cfg.ExcludeScopes {
		excluded[s] = true
	}
	var out []sdk.Finding
	for _, n := range graph.Packages() {
		if !n.Direct || excluded[n.Scope] || n.Version == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := purl.Parse(n.ID)
		if err != nil {
			continue
		}
		pkg := p.WithoutVersion().String()
		policy, err := domain.PolicyFor(a.cfg.Rules, pkg)
		if err != nil {
			return nil, err
		}
		if policy.Ignore {
			continue
		}
		current, err := deps.ParseVersion(n.Ecosystem, n.Version)
		if err != nil {
			continue
		}
		published, err := registry.Versions(ctx, *req.Project, pkg)
		if err != nil {
			if !errors.Is(err, deps.ErrPackageNotFound) {
				a.mu.Lock()
				a.warn = append(a.warn, sdk.Warning{Code: "registry-unavailable", Project: req.Project.ID,
					Message: fmt.Sprintf("updates of %s are unknown: %v", n.Name, err)})
				a.mu.Unlock()
			}
			continue
		}
		u := domain.Classify(current, published, policy)
		if !u.Any() {
			continue
		}
		out = append(out, finding(req, n, pkg, u))
	}
	return out, nil
}

func finding(req sdk.AnalyzeRequest, n deps.Node, pkg string, u domain.Updates) sdk.Finding {
	target := u.Latest
	return sdk.Finding{
		Analyzer: ID, Rule: "update-" + string(u.Type), Kind: sdk.KindOutdated, Severity: sdk.SeverityNone,
		Title:   fmt.Sprintf("%s update available", u.Type),
		Message: fmt.Sprintf("%s %s → %s available (%s)", n.Name, n.Version, target, u.Type),
		Project: req.Project.ID,
		Locations: []sdk.Location{{File: manifestLocation(req, n),
			Package: &sdk.PackageLocation{PURL: n.ID, Direct: true, Scope: n.Scope}}},
		Properties: map[string]any{
			"package": map[string]any{"name": n.Name, "version": n.Version, "ecosystem": string(n.Ecosystem)},
			"current": n.Version, "patch": u.Patch, "minor": u.Minor, "major": u.Major, "latest": u.Latest, "updateType": string(u.Type),
		},
		Remediations: []sdk.Remediation{{Description: fmt.Sprintf("update %s %s → %s", n.Name, n.Version, target),
			Group: "updates/" + n.Name, Package: &sdk.PackageChange{PURL: pkg, From: n.Version, To: target, Reason: string(u.Type) + " update"}}},
	}
}

func manifestLocation(req sdk.AnalyzeRequest, n deps.Node) *sdk.FileLocation {
	if len(req.Project.Manifests) == 0 {
		return &sdk.FileLocation{Path: req.Project.Dir}
	}
	needle := n.Name
	if i := strings.LastIndex(needle, ":"); i >= 0 {
		needle = needle[i+1:]
	}
	if base := n.Attributes["scalaBase"]; base != "" {
		needle = base
	}
	if req.Workspace != nil && req.Workspace.FS != nil {
		for _, m := range req.Project.Manifests {
			data, err := fs.ReadFile(req.Workspace.FS, m)
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(bytes.NewReader(data))
			for line := 1; sc.Scan(); line++ {
				if strings.Contains(strings.ToLower(sc.Text()), strings.ToLower(needle)) {
					return &sdk.FileLocation{Path: m, StartLine: line}
				}
			}
		}
	}
	return &sdk.FileLocation{Path: req.Project.Manifests[0]}
}
