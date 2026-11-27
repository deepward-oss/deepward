package depvuln

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const (
	root    = "project:maven:."
	spring  = "pkg:maven/org.springframework.boot/spring-boot-starter-web@2.5.0"
	jackson = "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1"
	log4j   = "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"
	junit   = "pkg:maven/junit/junit@4.13.1"
	safe    = "pkg:maven/org.apache.logging.log4j/log4j-api@2.17.2"
)

func graph(t *testing.T) *deps.Graph {
	t.Helper()
	g, err := deps.NewGraph(root, "test", []deps.Node{
		{ID: spring, Ecosystem: deps.Maven, Name: "org.springframework.boot:spring-boot-starter-web", Version: "2.5.0", Scope: "compile"},
		{ID: jackson, Ecosystem: deps.Maven, Name: "com.fasterxml.jackson.core:jackson-databind", Version: "2.12.1", Scope: "compile"},
		{ID: log4j, Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1", Scope: "compile"},
		{ID: junit, Ecosystem: deps.Maven, Name: "junit:junit", Version: "4.13.1", Scope: "test"},
		{ID: safe, Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-api", Version: "2.17.2", Scope: "compile"},
	}, []deps.Edge{{From: root, To: spring}, {From: spring, To: jackson}, {From: root, To: log4j}, {From: root, To: junit}, {From: root, To: safe}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func rng(introduced, fixed string) []deps.Range {
	return []deps.Range{{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: introduced}, {Fixed: fixed}}}}
}

func advisories() []deps.Advisory {
	return []deps.Advisory{
		{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Summary: "Remote code injection in Log4j",
			Severity:  []deps.Score{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H"}},
			Published: time.Date(2021, 12, 10, 0, 0, 0, 0, time.UTC), Details: strings.Repeat("d", 5000),
			References: []sdk.Reference{{Type: "FIX", URL: "https://github.com/apache/logging-log4j2/pull/608"}, {Type: "PACKAGE", URL: "https://x"}},
			Affected:   []deps.Affected{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Ranges: rng("2.0-beta9", "2.15.0")}}},
		{ID: "CVE-2021-45046", Summary: "Incomplete fix", DatabaseSeverity: "CRITICAL",
			Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Ranges: rng("2.0-beta9", "2.16.0")}}},
		{ID: "GHSA-57j2-w4cx-62h2", Aliases: []string{"CVE-2020-36518"}, DatabaseSeverity: "HIGH",
			Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "com.fasterxml.jackson.core:jackson-databind", Ranges: rng("0", "2.12.6.1")}}},
		{ID: "GHSA-junit", DatabaseSeverity: "MODERATE",
			Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "junit:junit", Ranges: []deps.Range{{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "0"}}}}}}},
		{ID: "GHSA-api", Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-api", Ranges: rng("2.0", "2.15.0")}}},
	}
}

type factsOf map[string]any

func (f factsOf) Fact(_ context.Context, name string) (any, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return nil, &sdk.FactError{Name: name}
}

func newAnalyzer(t *testing.T, offline bool, sources ...sdk.Plugin) *Analyzer {
	t.Helper()
	a := New()
	reg := &depstest.Ecosystem{FakeEcosystem: sdktest.FakeEcosystem{ID: "maven"}, Registry: &depstest.FakeRegistry{Versions: map[string][]string{
		"pkg:maven/org.springframework.boot/spring-boot-starter-web": {"2.4.0", "2.5.0", "2.5.1", "2.5.14", "2.6.0-M1", "2.6.0", "2.7.0", "3.0.0", "3.1.0"},
		"pkg:maven/org.apache.logging.log4j/log4j-core":              {"2.15.0", "2.16.0", "2.17.0", "2.17.1"},
	}}}
	host := &sdktest.FakeHost{Run: sdk.RunSettings{Offline: offline}, PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: sources, sdk.KindEcosystem: {reg},
	}}
	if err := a.Init(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	return a
}

func request(t *testing.T) sdk.AnalyzeRequest {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{"pom.xml": {Data: []byte("<project>\n  <dependencies>\n    <artifactId>spring-boot-starter-web</artifactId>\n    <artifactId>log4j-core</artifactId>\n")}}}
	return sdk.AnalyzeRequest{Project: &sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml"}}, Workspace: ws,
		Facts: factsOf{deps.FactDependencyGraph: graph(t)}}
}

func byRule(fs []sdk.Finding) map[string]sdk.Finding {
	m := map[string]sdk.Finding{}
	for _, f := range fs {
		m[f.Rule] = f
	}
	return m
}

func TestAnalyzeConformsAndMatches(t *testing.T) {
	osv := &depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: advisories()}
	ghsa := &depstest.FakeSource{ID: "ghsa", License: "CC-BY-4.0", Advisories: []deps.Advisory{{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"},
		Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Ranges: rng("2.0-beta9", "2.15.0")}}}}}
	a := newAnalyzer(t, false, osv, ghsa)
	found := sdktest.TestAnalyzer(t, a, request(t))
	m := byRule(found)
	if len(found) != 4 {
		t.Fatalf("findings: %d %v", len(found), keys(m))
	}
	l := m["CVE-2021-44228"]
	if l.Severity != sdk.SeverityCritical || strings.Join(l.Aliases, ",") != "GHSA-jfh8-c2jp-5v3q" || l.Properties["fixedIn"] != "2.16.0" {
		t.Errorf("log4shell: %+v", l)
	}
	if srcs := l.Properties["sources"].([]string); strings.Join(srcs, ",") != "ghsa,osv" {
		t.Errorf("dedup across sources: %v", srcs)
	}
	if l.PrimaryFile().Path != "pom.xml" || l.PrimaryFile().StartLine != 4 || !l.PrimaryPackage().Direct {
		t.Errorf("locations: %+v %+v", l.PrimaryFile(), l.PrimaryPackage())
	}
	if len(l.Remediations) != 1 || l.Remediations[0].Package.To != "2.16.0" || l.Remediations[0].Package.Pin {
		t.Errorf("direct remediation: %+v", l.Remediations)
	}
	if got := strings.Join(l.Remediations[0].Affects, ","); !strings.HasPrefix(got, "pkg:maven/org.apache.logging.log4j/log4j-core") {
		t.Errorf("direct upgrade affects the package: %s", got)
	}
	if !strings.HasSuffix(l.Properties["details"].(string), "…") || l.Properties["published"] != "2021-12-10" || l.Properties["cvss"] == nil {
		t.Error("properties")
	}
	if l.References[0].URL != "https://osv.dev/vulnerability/CVE-2021-44228" {
		t.Errorf("references: %+v", l.References)
	}
	if m["CVE-2021-45046"].Severity != sdk.SeverityCritical {
		t.Error("database severity fallback")
	}
	j := m["CVE-2020-36518"]
	if j.Severity != sdk.SeverityHigh || j.PrimaryPackage().Direct || j.PrimaryFile().StartLine != 3 {
		t.Errorf("transitive jackson: %+v", j)
	}
	if anc := j.Properties["ancestors"].([]string); anc[0] != "org.springframework.boot:spring-boot-starter-web@2.5.0" {
		t.Errorf("ancestors: %v", anc)
	}
	var tos []string
	for _, r := range j.Remediations {
		tos = append(tos, r.Package.To+map[bool]string{true: "(pin)", false: ""}[r.Package.Pin])
	}
	if strings.Join(tos, ",") != "2.5.1,2.5.14,2.6.0,2.7.0,3.0.0,2.12.6.1(pin)" {
		t.Errorf("transitive remediations: %v", tos)
	}
	if !strings.Contains(j.Properties["recommendation"].(string), "spring-boot-starter-web") {
		t.Error("recommendation")
	}
	if _, ok := m["GHSA-api"]; ok {
		t.Error("log4j-api 2.17.2 is outside the range")
	}
	junitF := m["GHSA-junit"]
	if junitF.Properties["noFixAvailable"] != true || junitF.Remediations != nil {
		t.Errorf("no fix: %+v", junitF)
	}
	ds := a.DataSources()
	if len(ds) != 2 || ds[0].License != "CC-BY-4.0" {
		t.Errorf("data sources: %+v", ds)
	}
}

func keys(m map[string]sdk.Finding) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestScopesAndEdgeCases(t *testing.T) {
	src := &depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: advisories()}
	a := newAnalyzer(t, false, src)
	a.cfg.ExcludeScopes = []string{"test"}
	found, err := a.Analyze(context.Background(), request(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byRule(found)["GHSA-junit"]; ok {
		t.Error("test scope excluded")
	}
	empty, _ := deps.NewGraph(root, "t", nil, nil)
	req := request(t)
	req.Facts = factsOf{deps.FactDependencyGraph: empty}
	if fs, err := a.Analyze(context.Background(), req); fs != nil || err != nil {
		t.Error("empty graph")
	}
	req.Facts = factsOf{}
	if _, err := a.Analyze(context.Background(), req); err == nil {
		t.Error("missing graph")
	}
	failing := newAnalyzer(t, false, &depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Err: errors.New("503")})
	if _, err := failing.Analyze(context.Background(), request(t)); err == nil || !strings.Contains(err.Error(), "advisory source osv: 503") {
		t.Errorf("source failure: %v", err)
	}
	noManifests := request(t)
	noManifests.Project.Manifests = nil
	fs, _ := a.Analyze(context.Background(), noManifests)
	if fs[0].PrimaryFile().Path != "." {
		t.Errorf("fallback location: %+v", fs[0].PrimaryFile())
	}
	if (&Config{MaxCandidates: -1}).Validate() == nil || (&Config{}).Validate() != nil {
		t.Error("validate")
	}
	if withoutVersion("garbage") != "garbage" {
		t.Error("withoutVersion fallback")
	}
}

func TestSourceSelection(t *testing.T) {
	a := New()
	err := a.Init(context.Background(), &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {&depstest.FakeSource{ID: "shady", License: "CC-BY-NC-4.0"}}}})
	if err == nil || !strings.Contains(err.Error(), "advisory source shady is refused") {
		t.Errorf("non-commercial license: %v", err)
	}
	if err := New().Init(context.Background(), &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {&depstest.FakeSource{ID: "nolicense"}}}}); err == nil {
		t.Error("unlicensed source")
	}
	online := &depstest.FakeSource{ID: "osv", License: "CC-BY-4.0"}
	missingDB := &depstest.FakeSource{ID: "osv-offline", License: "CC-BY-4.0"}
	withDB := &depstest.FakeSource{ID: "osv-offline", License: "CC-BY-4.0", HasData: true}
	off := New()
	err = off.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{Offline: true}, PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {online, missingDB.AsOffline()}}})
	if err == nil || !strings.Contains(err.Error(), "deepward db update") {
		t.Errorf("offline without database: %v", err)
	}
	off2 := New()
	_ = off2.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{Offline: true}, PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {online, withDB.AsOffline(), &sdktest.FakeAnalyzer{ID: "not-a-source"}}}})
	if len(off2.sources) != 1 || off2.sources[0].Info().ID != "osv-offline" {
		t.Errorf("offline sources: %v", off2.sources)
	}
	if off2.registry("maven") != nil {
		t.Error("no registry lookups offline")
	}
	on := New()
	_ = on.Init(context.Background(), &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {online, missingDB.AsOffline()}}})
	if len(on.sources) != 1 || on.sources[0].Info().ID != "osv" {
		t.Errorf("online skips unavailable offline source: %v", on.sources)
	}
	if New().registry("maven") != nil {
		t.Error("registry without host")
	}
	if fs, _ := New().Analyze(context.Background(), sdk.AnalyzeRequest{}); fs != nil {
		t.Error("workspace request")
	}
}

// TestThroughEngine runs the analyzer inside the real engine with a fake
// ecosystem that produces the graph.
func TestThroughEngine(t *testing.T) {
	reg := sdk.NewRegistry()
	eco := &depstest.Ecosystem{FakeEcosystem: sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml",
		Facts: map[string]func(context.Context, sdk.FactRequest) (any, error){
			deps.FactDependencyGraph: func(context.Context, sdk.FactRequest) (any, error) { return graph(t), nil },
		}}}
	_ = reg.Register(eco)
	_ = reg.Register(New())
	_ = reg.Register(&depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: advisories()})
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{"pom.xml": {Data: []byte("<project/>")}}}
	e, err := engine.New(context.Background(), engine.Options{Registry: reg, Workspace: ws, Config: engine.Config{FailOn: sdk.SeverityHigh}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 4 || res.Summary.BySeverity[sdk.SeverityCritical] != 2 || !res.Gate.Failed || len(res.DataSources) != 1 {
		t.Errorf("engine result: findings=%d summary=%+v gate=%+v sources=%+v", len(res.Findings), res.Summary.BySeverity, res.Gate, res.DataSources)
	}
	if res.Projects[0].Facts[deps.FactDependencyGraph] == nil {
		t.Error("graph embedded in the result")
	}
}

func TestNotBuiltPackages(t *testing.T) {
	g, err := deps.NewGraph(root, "test", []deps.Node{
		{ID: log4j, Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1", Scope: deps.ScopeNotBuilt},
		{ID: jackson, Ecosystem: deps.Maven, Name: "com.fasterxml.jackson.core:jackson-databind", Version: "2.12.1", Scope: deps.ScopeCompile},
		{ID: safe, Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-api", Version: "2.17.2", Scope: deps.ScopeNotBuilt},
	}, []deps.Edge{{From: root, To: log4j}, {From: root, To: jackson}, {From: root, To: safe}})
	if err != nil {
		t.Fatal(err)
	}
	req := request(t)
	req.Facts = factsOf{deps.FactDependencyGraph: g}
	a := newAnalyzer(t, false, &depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: advisories()})
	// Default: reported, marked not built, with real severity and fixes.
	found, err := a.Analyze(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	rules := byRule(found)
	if len(rules) != 3 || len(a.Warnings()) != 0 {
		t.Fatalf("findings: %v, warnings %d", keys(rules), len(a.Warnings()))
	}
	log4jFinding := rules["CVE-2021-44228"]
	if !log4jFinding.NotBuilt || log4jFinding.Properties["notBuilt"] != true || log4jFinding.Severity != sdk.SeverityCritical ||
		len(log4jFinding.Remediations) == 0 || log4jFinding.Locations[0].Package.Scope != deps.ScopeNotBuilt ||
		!strings.HasPrefix(log4jFinding.Properties["recommendation"].(string), "not urgent:") {
		t.Errorf("not-built finding: %+v", log4jFinding)
	}
	if rules["CVE-2020-36518"].NotBuilt {
		t.Error("built package must not be marked")
	}
	// warn: listed in one warning instead.
	a.cfg.NotBuilt = NotBuiltWarn
	found, _ = a.Analyze(context.Background(), req)
	if got := keys(byRule(found)); strings.Join(got, ",") != "CVE-2020-36518" {
		t.Errorf("warn mode reports built packages only: %v", got)
	}
	w := a.Warnings()
	if len(w) != 1 || w[0].Code != "not-built-skipped" || w[0].Project != "maven:." ||
		!strings.Contains(w[0].Message, "1 package(s)") || !strings.Contains(w[0].Message, "org.apache.logging.log4j:log4j-core@2.14.1 (") ||
		!strings.Contains(w[0].Message, "CVE-2021-44228") || !strings.Contains(w[0].Message, "CVE-2021-45046") {
		t.Errorf("warning: %+v", w)
	}
	for _, c := range []Config{{NotBuilt: "hide"}} {
		if c.Validate() == nil {
			t.Errorf("%q must be invalid", c.NotBuilt)
		}
	}
	if (&Config{NotBuilt: NotBuiltReport}).Validate() != nil || (&Config{NotBuilt: NotBuiltWarn}).Validate() != nil {
		t.Error("valid notBuilt values")
	}
}
