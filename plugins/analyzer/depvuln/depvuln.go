// Package depvuln is the dependency-vulnerabilities analyzer: it matches the
// dependency graph against advisory sources and reports vulnerabilities
// with fixed versions and remediations.
package depvuln

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/analyzer/depvuln/domain"
)

// ID is the plugin ID.
const ID = "dependency-vulnerabilities"

// DefaultAllowedLicenses are data licenses that permit commercial use.
var DefaultAllowedLicenses = []string{"CC-BY-4.0", "CC0-1.0", "Apache-2.0", "MIT", "BSD-3-Clause", "ODC-By-1.0"}

// Values of Config.NotBuilt.
const (
	NotBuiltReport = "report"
	NotBuiltWarn   = "warn"
)

// Config is plugins.dependency-vulnerabilities.
type Config struct {
	// ExcludeScopes drops dependencies of these scopes (e.g. [test]).
	ExcludeScopes []string `yaml:"excludeScopes" json:"excludeScopes"`
	// MaxCandidates bounds the direct-ancestor upgrade alternatives.
	MaxCandidates int `yaml:"maxCandidates" json:"maxCandidates"`
	// MaxPaths bounds the dependency paths reported per finding.
	MaxPaths int `yaml:"maxPaths" json:"maxPaths"`
	// NotBuilt decides what happens to advisories on packages that are in the
	// graph but not compiled (scope not-built): "report" (default) reports
	// them as findings marked not built, outside the quality gate; "warn"
	// lists them in one warning per project instead.
	NotBuilt string `yaml:"notBuilt" json:"notBuilt,omitempty"`
	// AllowedDataLicenses lists acceptable SPDX IDs of advisory data.
	AllowedDataLicenses []string `yaml:"allowedDataLicenses" json:"allowedDataLicenses"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	if c.MaxCandidates < 0 || c.MaxPaths < 0 {
		return errors.New("maxCandidates and maxPaths must be >= 0")
	}
	switch c.NotBuilt {
	case "", NotBuiltReport, NotBuiltWarn:
	default:
		return fmt.Errorf("notBuilt must be %q or %q", NotBuiltReport, NotBuiltWarn)
	}
	return nil
}

// Analyzer is the dependency-vulnerabilities analyzer.
type Analyzer struct {
	cfg     Config
	host    sdk.Host
	sources []deps.AdvisorySource
	mu      sync.Mutex
	used    map[string]bool
	warn    []sdk.Warning
}

// Warnings implements sdk.WarningReporter.
func (a *Analyzer) Warnings() []sdk.Warning {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sdk.Warning{}, a.warn...)
}

func init() { sdk.Register(New()) }

// New returns the analyzer with defaults.
func New() *Analyzer {
	return &Analyzer{cfg: Config{MaxCandidates: 5, MaxPaths: 5, AllowedDataLicenses: DefaultAllowedLicenses}, used: map[string]bool{}}
}

// Info implements sdk.Plugin.
func (a *Analyzer) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindAnalyzer, Version: sdk.BuiltinVersion,
		Description: "Finds vulnerable direct and transitive dependencies using open advisory databases"}
}

// ConfigTarget implements sdk.Configurable.
func (a *Analyzer) ConfigTarget() any { return &a.cfg }

// Scope implements sdk.Analyzer.
func (a *Analyzer) Scope() sdk.AnalyzerScope { return sdk.ScopeProject }

// Requires implements sdk.Analyzer.
func (a *Analyzer) Requires() []string { return []string{deps.FactDependencyGraph} }

// Init implements sdk.Initializer: it selects the advisory sources and
// refuses sources whose data license is not allowed.
func (a *Analyzer) Init(_ context.Context, h sdk.Host) error {
	a.host = h
	allowed := map[string]bool{}
	for _, l := range a.cfg.AllowedDataLicenses {
		allowed[strings.ToUpper(l)] = true
	}
	offline := h.Settings().Offline
	a.sources = nil
	for _, p := range h.Plugins(deps.KindAdvisorySource) {
		src, ok := p.(deps.AdvisorySource)
		if !ok {
			continue
		}
		lic := src.DataLicense().SPDX
		if lic == "" || !allowed[strings.ToUpper(lic)] {
			return fmt.Errorf("advisory source %s is refused: its data license %q is not in allowedDataLicenses (disable it with plugins.%s.enabled: false)",
				p.Info().ID, lic, p.Info().ID)
		}
		off, isOffline := src.(deps.OfflineSource)
		switch {
		case offline && (!isOffline || !off.Available()):
			continue
		case !offline && isOffline && !off.Available():
			continue
		}
		a.sources = append(a.sources, src)
	}
	if offline && len(a.sources) == 0 {
		return errors.New("offline mode needs a local vulnerability database: run `deepward db update` first (or configure plugins.osv-offline.dir)")
	}
	return nil
}

// DataSources implements sdk.DataSourceReporter.
func (a *Analyzer) DataSources() []sdk.DataSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []sdk.DataSource
	for _, s := range a.sources {
		if !a.used[s.Info().ID] {
			continue
		}
		l := s.DataLicense()
		out = append(out, sdk.DataSource{Name: s.Info().ID, License: l.SPDX, Attribution: l.Attribution, URL: l.URL, Freshness: s.Freshness()})
	}
	return out
}

type pkgKey struct {
	eco  deps.Ecosystem
	name string
}

func keyOf(eco deps.Ecosystem, name string) pkgKey {
	if eco == deps.NuGet {
		name = strings.ToLower(name)
	}
	return pkgKey{eco, name}
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
	excluded := map[string]bool{}
	for _, s := range a.cfg.ExcludeScopes {
		excluded[s] = true
	}
	var nodes []deps.Node
	seen := map[deps.PackageQuery]bool{}
	var queries []deps.PackageQuery
	for _, n := range graph.Packages() {
		if excluded[n.Scope] || n.Version == "" || n.Ecosystem == "" {
			continue
		}
		nodes = append(nodes, n)
		q := deps.PackageQuery{Ecosystem: n.Ecosystem, Name: n.Name, Version: n.Version}
		if !seen[q] {
			seen[q] = true
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}
	byPkg := map[pkgKey][]deps.Advisory{}
	for _, src := range a.sources {
		advs, err := src.Query(ctx, queries)
		if err != nil {
			return nil, fmt.Errorf("advisory source %s: %w", src.Info().ID, err)
		}
		a.mu.Lock()
		a.used[src.Info().ID] = true
		a.mu.Unlock()
		for _, adv := range advs {
			if adv.Source == "" {
				adv.Source = src.Info().ID
			}
			for _, af := range adv.Affected {
				k := keyOf(af.Ecosystem, af.Name)
				byPkg[k] = append(byPkg[k], adv)
			}
		}
	}
	registry := a.registry(req.Project.Ecosystem)
	var findings []sdk.Finding
	var skipped []string
	for _, n := range nodes {
		ver, err := deps.ParseVersion(n.Ecosystem, n.Version)
		if err != nil {
			continue
		}
		var matched []deps.Advisory
		ids := map[string]bool{}
		for _, adv := range byPkg[keyOf(n.Ecosystem, n.Name)] {
			key := adv.Source + "\x00" + adv.ID
			if ids[key] || !domain.Affects(adv, n.Ecosystem, n.Name, ver) {
				continue
			}
			ids[key] = true
			matched = append(matched, adv)
		}
		if len(matched) == 0 {
			continue
		}
		if n.Scope == deps.ScopeNotBuilt && a.cfg.NotBuilt == NotBuiltWarn {
			var primary []string
			for _, grp := range domain.Deduplicate(matched) {
				primary = append(primary, grp.Primary())
			}
			skipped = append(skipped, fmt.Sprintf("%s@%s (%s)", n.Name, n.Version, strings.Join(primary, ", ")))
			continue
		}
		findings = append(findings, a.findings(ctx, req, graph, n, ver, matched, registry)...)
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		a.mu.Lock()
		a.warn = append(a.warn, sdk.Warning{Code: "not-built-skipped", Plugin: ID, Project: req.Project.ID,
			Message: fmt.Sprintf("advisories on %d package(s) that are not compiled into the project were not reported (notBuilt: warn): %s",
				len(skipped), strings.Join(skipped, "; "))})
		a.mu.Unlock()
	}
	return findings, nil
}

func (a *Analyzer) registry(ecosystem string) deps.VersionRegistry {
	if a.host == nil || a.host.Settings().Offline {
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

func withoutVersion(id string) string {
	p, err := purl.Parse(id)
	if err != nil {
		return id
	}
	return p.WithoutVersion().String()
}

func (a *Analyzer) findings(ctx context.Context, req sdk.AnalyzeRequest, g *deps.Graph, n deps.Node, ver deps.Version, matched []deps.Advisory, registry deps.VersionRegistry) []sdk.Finding {
	pkg := withoutVersion(n.ID)
	var published []string
	if registry != nil {
		published, _ = registry.Versions(ctx, *req.Project, pkg)
	}
	fixed, hasFix := domain.FixedVersion(matched, n.Ecosystem, n.Name, ver, published)
	var ancestors []string
	if !n.Direct {
		ancestors = g.DirectAncestors(n.ID)
	}
	// up is the direct ancestor to upgrade (the closest one, unless a farther
	// one is found to bring the fix) and what its newer versions bring.
	var up probe
	if len(ancestors) > 0 && hasFix {
		up = upgradeAncestor(ctx, *req.Project, g, registry, n, ancestors, matched)
	} else if len(ancestors) > 0 {
		up.ancestor, _ = g.Node(ancestors[0])
	}
	remediations := a.remediations(ctx, req, g, n, pkg, fixed, hasFix, up, registry)
	paths := g.Paths(n.ID, a.cfg.MaxPaths)
	declaring := n
	if up.ancestor.ID != "" {
		declaring = up.ancestor
	}
	var preferred []string
	if m, ok := g.DeclaringModule(declaring.ID, n.ID); ok && m.Attributes["manifest"] != "" {
		preferred = []string{m.Attributes["manifest"]}
	}
	fileLoc := manifestLocation(req, declaring, preferred...)
	var out []sdk.Finding
	for _, grp := range domain.Deduplicate(matched) {
		rating := domain.Rate(grp.Advisories)
		primary := grp.Primary()
		summary := firstSummary(grp.Advisories)
		f := sdk.Finding{
			Analyzer: ID, Rule: primary, Kind: sdk.KindVulnerability, Severity: rating.Severity, Title: summary,
			Message: fmt.Sprintf("%s %s is affected by %s", n.Name, n.Version, primary),
			Project: req.Project.ID, Aliases: grp.Aliases(),
		}
		if summary != "" {
			f.Message += ": " + summary
		}
		f.Locations = append(f.Locations, sdk.Location{File: fileLoc,
			Package: &sdk.PackageLocation{PURL: n.ID, Paths: paths, Direct: n.Direct, Scope: n.Scope, Modules: g.ModuleRefs(n.ID)}})
		f.References = references(grp)
		props := map[string]any{
			"package":   map[string]any{"name": n.Name, "version": n.Version, "ecosystem": string(n.Ecosystem)},
			"sources":   grp.Sources(),
			"ids":       grp.IDs,
			"affected":  affectedText(grp.Advisories, n),
			"published": earliest(grp.Advisories),
		}
		if rating.Score > 0 {
			props["cvss"] = map[string]any{"score": rating.Score, "vector": rating.Vector, "version": rating.Version}
		}
		if hasFix {
			props["fixedIn"] = fixed.String()
		} else {
			props["noFixAvailable"] = true
		}
		if len(ancestors) > 0 {
			names := make([]string, 0, len(ancestors))
			for _, anc := range ancestors {
				if an, ok := g.Node(anc); ok {
					names = append(names, an.Name+"@"+an.Version)
				}
			}
			props["ancestors"] = names
		}
		if up.outcome == outcomeFound {
			props["ancestorUpgrade"] = map[string]any{"package": up.ancestor.Name, "from": up.ancestor.Version, "to": up.to, "brings": up.brings}
		}
		props["recommendation"] = recommendation(n, fixed, hasFix, up, req.Project.Ecosystem)
		if n.Scope == deps.ScopeNotBuilt {
			f.NotBuilt = true
			props["notBuilt"] = true
			props["recommendation"] = "not urgent: " + n.Name + " is resolved but not compiled into the project, so the vulnerable code does not ship. " +
				"Update it to keep the dependency files clean for other scanners: " + props["recommendation"].(string)
		}
		if d := details(grp.Advisories); d != "" {
			props["details"] = d
		}
		f.Properties = props
		f.Remediations = remediations
		out = append(out, f)
	}
	return out
}

func (a *Analyzer) remediations(ctx context.Context, req sdk.AnalyzeRequest, g *deps.Graph, n deps.Node, pkg string, fixed deps.Version, hasFix bool,
	up probe, registry deps.VersionRegistry) []sdk.Remediation {
	if !hasFix {
		return nil
	}
	group := "security/" + n.Name
	if n.Direct {
		return []sdk.Remediation{{Description: fmt.Sprintf("upgrade %s %s → %s", n.Name, n.Version, fixed), Group: group, Affects: g.Affected(n.ID),
			Package: &sdk.PackageChange{PURL: pkg, From: n.Version, To: fixed.String(), Reason: "fixes vulnerabilities"}}}
	}
	var out []sdk.Remediation
	anc := up.ancestor
	switch {
	case anc.ID == "":
	case up.outcome == outcomeFound:
		// The published dependencies say which ancestor version brings the
		// fix; deepward fix still verifies it against the whole project.
		out = append(out, sdk.Remediation{Description: fmt.Sprintf("upgrade %s %s → %s to bring %s %s", anc.Name, anc.Version, up.to, n.Name, up.brings),
			Group: group, Affects: g.Affected(anc.ID),
			Package: &sdk.PackageChange{PURL: withoutVersion(anc.ID), From: anc.Version, To: up.to, Reason: "brings a fixed " + n.Name}})
	case up.outcome == outcomeUnknown && registry != nil && a.cfg.MaxCandidates > 0:
		// Unknown: offer the next releases for deepward fix to try.
		out = append(out, a.blindCandidates(ctx, req, g, anc, n, fixed, group, registry)...)
	}
	out = append(out, sdk.Remediation{Description: fmt.Sprintf("pin transitive %s %s → %s", n.Name, n.Version, fixed), Group: group, Affects: g.Affected(n.ID),
		Package: &sdk.PackageChange{PURL: pkg, From: n.Version, To: fixed.String(), Pin: true, Reason: "fixes vulnerabilities", Targets: pinTargets(g, n.ID)}})
	return out
}

// blindCandidates lists the next releases of the ancestor, for when what they
// bring could not be checked.
func (a *Analyzer) blindCandidates(ctx context.Context, req sdk.AnalyzeRequest, g *deps.Graph, anc, n deps.Node, fixed deps.Version, group string,
	registry deps.VersionRegistry) []sdk.Remediation {
	cur, err := deps.ParseVersion(anc.Ecosystem, anc.Version)
	if err != nil {
		return nil
	}
	ancPkg := withoutVersion(anc.ID)
	vs, _ := registry.Versions(ctx, *req.Project, ancPkg)
	var cands []deps.Version
	for _, raw := range vs {
		if v, err := deps.ParseVersion(anc.Ecosystem, raw); err == nil && cur.Less(v) && !v.IsPrerelease() {
			cands = append(cands, v)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Less(cands[j]) })
	if len(cands) > a.cfg.MaxCandidates {
		cands = cands[:a.cfg.MaxCandidates]
	}
	var out []sdk.Remediation
	affects := g.Affected(anc.ID)
	for _, c := range cands {
		out = append(out, sdk.Remediation{Description: fmt.Sprintf("upgrade %s %s → %s to bring %s ≥ %s", anc.Name, anc.Version, c, n.Name, fixed),
			Group: group, Affects: affects, Package: &sdk.PackageChange{PURL: ancPkg, From: anc.Version, To: c.String(), Reason: "brings a fixed " + n.Name}})
	}
	return out
}

func recommendation(n deps.Node, fixed deps.Version, hasFix bool, up probe, ecosystem string) string {
	if !hasFix {
		return "no fixed version is available yet; consider removing the dependency or suppressing the finding with a reason"
	}
	if n.Direct || up.ancestor.ID == "" {
		return fmt.Sprintf("upgrade %s to %s", n.Name, fixed)
	}
	return probeRecommendation(up, n, fixed, ecosystem)
}

func firstSummary(advs []deps.Advisory) string {
	for _, a := range advs {
		if a.Summary != "" {
			return a.Summary
		}
	}
	return ""
}

func details(advs []deps.Advisory) string {
	for _, a := range advs {
		if a.Details != "" {
			d := a.Details
			if len(d) > 4000 {
				d = d[:4000] + "…"
			}
			return d
		}
	}
	return ""
}

func earliest(advs []deps.Advisory) string {
	var best string
	for _, a := range advs {
		if a.Published.IsZero() {
			continue
		}
		s := a.Published.UTC().Format("2006-01-02")
		if best == "" || s < best {
			best = s
		}
	}
	return best
}

func affectedText(advs []deps.Advisory, n deps.Node) []string {
	set := map[string]bool{}
	for _, adv := range advs {
		for _, af := range domain.AffectedEntries(adv, n.Ecosystem, n.Name) {
			for _, r := range af.Ranges {
				var parts []string
				for _, e := range r.Events {
					switch {
					case e.Introduced != "":
						parts = append(parts, ">="+e.Introduced)
					case e.Fixed != "":
						parts = append(parts, "<"+e.Fixed)
					case e.LastAffected != "":
						parts = append(parts, "<="+e.LastAffected)
					}
				}
				if len(parts) > 0 {
					set[strings.Join(parts, ", ")] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func references(g domain.Group) []sdk.Reference {
	var out []sdk.Reference
	seen := map[string]bool{}
	add := func(r sdk.Reference) {
		if r.URL != "" && !seen[r.URL] {
			seen[r.URL] = true
			out = append(out, r)
		}
	}
	for _, id := range g.IDs {
		if strings.HasPrefix(id, "GHSA-") || strings.HasPrefix(id, "CVE-") {
			add(sdk.Reference{Type: "ADVISORY", URL: "https://osv.dev/vulnerability/" + id})
		}
	}
	for _, a := range g.Advisories {
		for _, r := range a.References {
			if r.Type == "ADVISORY" || r.Type == "FIX" || r.Type == "WEB" || r.Type == "REPORT" {
				add(r)
			}
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// manifestLocation finds the manifest line that declares a dependency (best
// effort: the first line mentioning the artifact name), searching the
// preferred manifests (the declaring module's) first.
func manifestLocation(req sdk.AnalyzeRequest, declaring deps.Node, preferred ...string) *sdk.FileLocation {
	if len(req.Project.Manifests) == 0 {
		return &sdk.FileLocation{Path: req.Project.Dir}
	}
	needle := declaring.Name
	if i := strings.LastIndex(needle, ":"); i >= 0 {
		needle = needle[i+1:]
	}
	if base := declaring.Attributes["scalaBase"]; base != "" {
		needle = base
	}
	if req.Workspace != nil && req.Workspace.FS != nil && needle != "" {
		for _, m := range append(append([]string{}, preferred...), req.Project.Manifests...) {
			data, err := fs.ReadFile(req.Workspace.FS, m)
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(bytes.NewReader(data))
			line := 0
			for sc.Scan() {
				line++
				if strings.Contains(strings.ToLower(sc.Text()), strings.ToLower(needle)) {
					return &sdk.FileLocation{Path: m, StartLine: line}
				}
			}
		}
	}
	return &sdk.FileLocation{Path: req.Project.Manifests[0]}
}

// pinTargets lists the manifests of the modules through which a package
// enters the project: a pin there reaches every module that depends on them.
func pinTargets(g *deps.Graph, id string) []string {
	var out []string
	for _, m := range g.EntryModules(id) {
		if n, ok := g.Node(m); ok && n.Attributes["manifest"] != "" {
			out = append(out, n.Attributes["manifest"])
		}
	}
	return out
}
