package html

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

type count struct {
	Name  string
	Count int
}

type projectRow struct {
	sdk.ProjectResult
	Findings int
}

type vulnRow struct {
	Severity       string
	Rank           int
	ID             string
	Aliases        []string
	Package        string
	Version        string
	Project        string
	Ecosystem      string
	Direct         bool
	Scope          string
	FixedIn        string
	NoFix          bool
	Title          string
	Details        string
	Score          string
	Vector         string
	Affected       []string
	Chains         [][]hop
	Recommendation string
	References     []sdk.Reference
	Location       string
	Analyzer       string
	Sources        string
	Published      string
	Search         string
	NotBuilt       bool
}

// vulnGroup is one vulnerable package version of a project with all the
// advisories that affect it: they share the dependency path, the fixed
// version and the recommended action, so the report shows them as one row.
type vulnGroup struct {
	Severity       string
	Rank           int
	Package        string
	Version        string
	Project        string
	Ecosystem      string
	Direct         bool
	Scope          string
	FixedIn        string
	NoFix          bool
	Chains         [][]hop
	Recommendation string
	Location       string
	NotBuilt       bool
	Search         string
	// Score is the CVSS score of the most severe advisory.
	Score string
	// Findings lists the severity of every advisory, for filters and counts.
	Findings   string
	Advisories []vulnRow
}

// IDs returns the advisory identifiers of the group.
func (g vulnGroup) IDs() []string {
	out := make([]string, 0, len(g.Advisories))
	for _, a := range g.Advisories {
		out = append(out, a.ID)
	}
	return out
}

// groupVulns merges advisories by project, package and version, keeping the
// order of first appearance; advisories inside a group are sorted by
// severity, then ID, and the group takes the highest severity.
func groupVulns(rows []vulnRow) []vulnGroup {
	var out []vulnGroup
	index := map[string]int{}
	for _, r := range rows {
		k := r.Project + "\x00" + r.Package + "\x00" + r.Version
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, vulnGroup{Package: r.Package, Version: r.Version, Project: r.Project, Ecosystem: r.Ecosystem, Direct: r.Direct,
				Scope: r.Scope, FixedIn: r.FixedIn, NoFix: r.NoFix, Chains: r.Chains, Recommendation: r.Recommendation, Location: r.Location,
				NotBuilt: r.NotBuilt, Rank: -1})
		}
		g := &out[i]
		g.Advisories = append(g.Advisories, r)
		if r.Rank > g.Rank {
			g.Rank, g.Severity = r.Rank, r.Severity
		}
		g.FixedIn = firstNonEmpty(g.FixedIn, r.FixedIn)
		g.Recommendation = firstNonEmpty(g.Recommendation, r.Recommendation)
		g.NoFix = g.NoFix && r.NoFix
	}
	for i := range out {
		g := &out[i]
		sort.SliceStable(g.Advisories, func(a, b int) bool {
			if g.Advisories[a].Rank != g.Advisories[b].Rank {
				return g.Advisories[a].Rank > g.Advisories[b].Rank
			}
			return g.Advisories[a].ID < g.Advisories[b].ID
		})
		sevs := make([]string, 0, len(g.Advisories))
		search := []string{}
		for _, a := range g.Advisories {
			sevs = append(sevs, a.Severity)
			search = append(search, a.Search)
		}
		// The score shown next to the severity is that of the top advisory.
		g.Score = g.Advisories[0].Score
		g.Findings = strings.Join(sevs, " ")
		g.Search = strings.Join(search, " ")
	}
	return out
}

// hop is one package on a dependency path.
type hop struct {
	Name, Version string
	// Target marks the vulnerable package at the end of the path.
	Target bool
}

func hopOf(id string, target bool) hop {
	p, err := purl.Parse(id)
	if err != nil {
		return hop{Name: id, Target: target}
	}
	return hop{Name: p.Coordinates(), Version: p.Version, Target: target}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

type outdatedRow struct {
	Package    string
	Current    string
	Patch      string
	Minor      string
	Major      string
	Latest     string
	UpdateType string
	Project    string
	Ecosystem  string
	Direct     bool
	Search     string
}

type genericRow struct {
	Severity  string
	Rank      int
	Rule      string
	Title     string
	Message   string
	Location  string
	Analyzer  string
	Tool      string
	Project   string
	Ecosystem string
	Search    string
}

type kindGroup struct {
	Kind string
	Rows []genericRow
}

type suppressedRow struct {
	Rule    string
	Package string
	Project string
	Reason  string
	Expires string
}

type view struct {
	Res          *sdk.ScanResult
	Generated    string
	Severities   []count
	Analyzers    []count
	ProjectStats []count
	Projects     []projectRow
	Vulns        []vulnGroup
	// VulnAdvisories is the number of advisories in Vulns.
	VulnAdvisories int
	VulnCounts     []count
	// NotBuiltVulns are vulnerabilities in packages that are not built,
	// shown in their own section.
	NotBuiltVulns      []vulnGroup
	NotBuiltAdvisories int
	NotBuiltCounts     []count
	NotBuilt           int
	Outdated           []outdatedRow
	Generic            []kindGroup
	Suppressed         []suppressedRow
	FailedRuns         []sdk.PluginRun
	ProjectIDs         []string
	Ecosystems         []string
}

var displaySeverities = []sdk.Severity{sdk.SeverityCritical, sdk.SeverityHigh, sdk.SeverityMedium, sdk.SeverityLow, sdk.SeverityUnknown}

func ecosystemOf(projectID string) string {
	eco, _, _ := strings.Cut(projectID, ":")
	return eco
}

// short renders a purl as "coordinates@version".
func short(id string) string {
	p, err := purl.Parse(id)
	if err != nil {
		return id
	}
	s := p.Coordinates()
	if p.Version != "" {
		s += "@" + p.Version
	}
	return s
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func strs(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

func sub(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func location(f sdk.Finding) string {
	if fl := f.PrimaryFile(); fl != nil {
		if fl.StartLine > 0 {
			return fmt.Sprintf("%s:%d", fl.Path, fl.StartLine)
		}
		return fl.Path
	}
	if p := f.PrimaryPackage(); p != nil {
		return short(p.PURL)
	}
	return f.Project
}

func buildView(res *sdk.ScanResult) *view {
	v := &view{Res: res}
	if !res.Run.FinishedAt.IsZero() {
		v.Generated = res.Run.FinishedAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	// Severity cards count findings in built code; not-built ones get their own card.
	built := res.Summary.BuiltBySeverity()
	for _, s := range displaySeverities {
		v.Severities = append(v.Severities, count{Name: string(s), Count: built[s]})
	}
	v.NotBuilt = res.Summary.NotBuilt
	for _, name := range sortedKeys(res.Summary.ByAnalyzer) {
		v.Analyzers = append(v.Analyzers, count{Name: name, Count: res.Summary.ByAnalyzer[name]})
	}
	stats := map[sdk.ProjectStatus]int{}
	ecos := map[string]bool{}
	for _, p := range res.Projects {
		stats[p.Status]++
		v.Projects = append(v.Projects, projectRow{ProjectResult: p, Findings: res.Summary.ByProject[p.ID]})
		v.ProjectIDs = append(v.ProjectIDs, p.ID)
		ecos[ecosystemOf(p.ID)] = true
	}
	for _, s := range []sdk.ProjectStatus{sdk.ProjectOK, sdk.ProjectSkipped, sdk.ProjectFailed} {
		v.ProjectStats = append(v.ProjectStats, count{Name: string(s), Count: stats[s]})
	}
	v.Ecosystems = sortedKeys(ecos)
	vulnCounts, notBuiltCounts := map[string]int{}, map[string]int{}
	var vulns, notBuiltVulns []vulnRow
	generic := map[sdk.FindingKind][]genericRow{}
	for _, f := range res.Findings {
		if f.Suppression != nil {
			row := suppressedRow{Rule: f.Rule, Project: f.Project, Reason: f.Suppression.Reason, Expires: f.Suppression.Expires}
			if p := f.PrimaryPackage(); p != nil {
				row.Package = short(p.PURL)
			}
			v.Suppressed = append(v.Suppressed, row)
			continue
		}
		sev := f.Severity.Normalize()
		switch f.Kind {
		case sdk.KindVulnerability:
			row := vulnRowOf(f)
			if row.NotBuilt {
				notBuiltCounts[row.Severity]++
				notBuiltVulns = append(notBuiltVulns, row)
				break
			}
			vulnCounts[row.Severity]++
			vulns = append(vulns, row)
		case sdk.KindOutdated:
			v.Outdated = append(v.Outdated, outdatedRowOf(f))
		default:
			r := genericRow{Severity: string(sev), Rank: sev.Rank(), Rule: f.Rule, Title: f.Title, Message: f.Message, Location: location(f),
				Analyzer: f.Analyzer, Tool: f.Tool, Project: f.Project, Ecosystem: ecosystemOf(f.Project)}
			r.Search = strings.ToLower(strings.Join([]string{f.Rule, f.Message, r.Location, f.Tool}, " "))
			generic[f.Kind] = append(generic[f.Kind], r)
		}
	}
	v.Vulns, v.VulnAdvisories = groupVulns(vulns), len(vulns)
	v.NotBuiltVulns, v.NotBuiltAdvisories = groupVulns(notBuiltVulns), len(notBuiltVulns)
	for _, s := range displaySeverities {
		v.VulnCounts = append(v.VulnCounts, count{Name: string(s), Count: vulnCounts[string(s)]})
		v.NotBuiltCounts = append(v.NotBuiltCounts, count{Name: string(s), Count: notBuiltCounts[string(s)]})
	}
	kinds := make([]string, 0, len(generic))
	for k := range generic {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		v.Generic = append(v.Generic, kindGroup{Kind: k, Rows: generic[sdk.FindingKind(k)]})
	}
	for _, r := range res.PluginRuns {
		if r.Status == sdk.RunFailed {
			v.FailedRuns = append(v.FailedRuns, r)
		}
	}
	return v
}

func vulnRowOf(f sdk.Finding) vulnRow {
	sev := f.Severity.Normalize()
	r := vulnRow{NotBuilt: f.NotBuilt, Severity: string(sev), Rank: sev.Rank(), ID: f.Rule, Aliases: f.Aliases, Project: f.Project, Ecosystem: ecosystemOf(f.Project),
		Title: f.Title, Analyzer: f.Analyzer, References: f.References, Location: location(f)}
	props := f.Properties
	pkg := sub(props, "package")
	r.Package, r.Version = str(pkg, "name"), str(pkg, "version")
	if p := f.PrimaryPackage(); p != nil {
		r.Direct, r.Scope = p.Direct, p.Scope
		if r.Package == "" {
			h := hopOf(p.PURL, false)
			r.Package, r.Version = h.Name, firstNonEmpty(r.Version, h.Version)
		}
		for _, path := range p.Paths {
			chain := make([]hop, 0, len(path))
			for i, id := range path {
				chain = append(chain, hopOf(id, i == len(path)-1))
			}
			r.Chains = append(r.Chains, chain)
		}
	}
	r.FixedIn = str(props, "fixedIn")
	r.NoFix = str(props, "noFixAvailable") == "true"
	r.Details = str(props, "details")
	r.Recommendation = str(props, "recommendation")
	r.Affected = strs(props, "affected")
	r.Sources = strings.Join(strs(props, "sources"), ", ")
	r.Published = str(props, "published")
	if c := sub(props, "cvss"); len(c) > 0 {
		r.Score, r.Vector = str(c, "score"), str(c, "vector")
	}
	r.Search = strings.ToLower(strings.Join(append([]string{r.ID, r.Package, r.Version, r.Title}, r.Aliases...), " "))
	return r
}

func outdatedRowOf(f sdk.Finding) outdatedRow {
	props := f.Properties
	r := outdatedRow{Current: str(props, "current"), Patch: str(props, "patch"), Minor: str(props, "minor"), Major: str(props, "major"),
		Latest: str(props, "latest"), UpdateType: str(props, "updateType"), Project: f.Project, Ecosystem: ecosystemOf(f.Project)}
	r.Package = str(sub(props, "package"), "name")
	if p := f.PrimaryPackage(); p != nil {
		r.Direct = p.Direct
		if r.Package == "" {
			r.Package = short(p.PURL)
		}
	}
	r.Search = strings.ToLower(r.Package + " " + r.Current + " " + r.Latest)
	return r
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
