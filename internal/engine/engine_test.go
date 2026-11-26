package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func workspace() *sdk.Workspace {
	return &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"api/pom.xml":   {Data: []byte("<project/>")},
		"web/pom.xml":   {Data: []byte("<project/>")},
		"api/README.md": {Data: []byte("x")},
	}}
}

func parseConfig(t *testing.T, src string) Config {
	t.Helper()
	var c Config
	if src == "" {
		return c
	}
	doc, err := strictyaml.Parse([]byte(src), "deepward.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := strictyaml.Decode(doc, &c, "", "deepward.yaml"); err != nil {
		t.Fatal(err)
	}
	return c
}

type graphFact struct{ n int }

func (g *graphFact) ReportFact() any { return map[string]int{"nodes": g.n} }

func newEngine(t *testing.T, cfg string, plugins ...sdk.Plugin) (*Engine, error) {
	t.Helper()
	reg := sdk.NewRegistry()
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return New(context.Background(), Options{Registry: reg, Config: parseConfig(t, cfg), Workspace: workspace(),
		Clock: sdk.FixedClock{T: now}, ConfigFile: "deepward.yaml", Tool: sdk.ToolInfo{Name: "deepward", Version: "test"}})
}

func mustEngine(t *testing.T, cfg string, plugins ...sdk.Plugin) *Engine {
	t.Helper()
	e, err := newEngine(t, cfg, plugins...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func eco() *sdktest.FakeEcosystem {
	return &sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml", Facts: map[string]func(context.Context, sdk.FactRequest) (any, error){
		"dependency-graph": func(context.Context, sdk.FactRequest) (any, error) { return &graphFact{n: 3}, nil },
	}}
}

func findingAnalyzer(id string, sev sdk.Severity, needs ...string) *sdktest.FakeAnalyzer {
	return &sdktest.FakeAnalyzer{ID: id, Needs: needs, Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		f := sdktest.SimpleFinding("", "RULE-"+id, sev, "")
		return []sdk.Finding{f}, nil
	}}
}

func TestScanMemoizesFactsAcrossAnalyzers(t *testing.T) {
	ecosystem := eco()
	e := mustEngine(t, "", ecosystem, findingAnalyzer("vulns", sdk.SeverityHigh, "dependency-graph"),
		findingAnalyzer("outdated", sdk.SeverityNone, "dependency-graph"), &sdktest.FakeReporter{ID: "txt", Name: "txt"})
	res, err := e.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := ecosystem.Calls.Load(); got != 2 {
		t.Errorf("graph produced %d times for 2 projects, want once per project", got)
	}
	if len(res.Projects) != 2 || res.Projects[0].ID != "maven:api" || res.Projects[0].Status != sdk.ProjectOK {
		t.Fatalf("projects: %+v", res.Projects)
	}
	if res.Projects[0].Facts["dependency-graph"] == nil {
		t.Error("reportable fact not embedded")
	}
	if len(res.Findings) != 4 || res.Findings[0].Analyzer != "vulns" || res.Findings[0].Project != "maven:api" || res.Findings[0].Fingerprint == "" {
		t.Errorf("findings: %+v", res.Findings)
	}
	if res.Tool.SDKVersion != sdk.SDKVersion || res.Run.StartedAt != now || res.Run.Root != "/repo" {
		t.Error("metadata")
	}
	var buf bytes.Buffer
	if err := e.Render(context.Background(), res, "txt", &buf); err != nil || buf.String() != "txt: 4 findings\n" {
		t.Errorf("render: %q %v", buf.String(), err)
	}
	if err := e.Render(context.Background(), res, "pdf", &buf); err == nil {
		t.Error("unknown format")
	}
}

func TestMissingFactSkipsOnlyDependentAnalyzers(t *testing.T) {
	ecosystem := eco()
	ecosystem.Facts["dependency-graph"] = func(_ context.Context, req sdk.FactRequest) (any, error) {
		if req.Project.Dir == "web" {
			return nil, &sdk.ExitError{Tool: "sbt", ExitCode: 1, Tail: []string{"[error] boom"}}
		}
		return &graphFact{}, nil
	}
	readme := &sdktest.FakeAnalyzer{ID: "readme", Needs: []string{sdk.FactSourceTree}, Func: func(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		tree, err := sdk.GetFact(ctx, req.Facts, sdk.SourceTreeKey)
		if err != nil {
			return nil, err
		}
		return []sdk.Finding{sdktest.SimpleFinding("", "files", sdk.SeverityLow, "")}, func() error {
			if len(tree.Files) == 0 {
				return errors.New("no files")
			}
			return nil
		}()
	}}
	e := mustEngine(t, "", ecosystem, findingAnalyzer("vulns", sdk.SeverityHigh, "dependency-graph"), readme)
	res, _ := e.Scan(context.Background())
	var skipped, readmeRuns int
	for _, r := range res.PluginRuns {
		if r.Plugin == "vulns" && r.Status == sdk.RunSkipped && r.Reason == "fact-unavailable: dependency-graph" && r.Project == "maven:web" {
			skipped++
		}
		if r.Plugin == "readme" && r.Status == sdk.RunOK {
			readmeRuns++
		}
	}
	if skipped != 1 || readmeRuns != 2 {
		t.Errorf("runs: %+v", res.PluginRuns)
	}
	web := res.Projects[1]
	if web.Status != sdk.ProjectFailed || web.Output[0] != "[error] boom" || !Failed(res) {
		t.Errorf("web project: %+v", web)
	}
}

func TestToolNotFoundSkipsProject(t *testing.T) {
	ecosystem := eco()
	ecosystem.Facts["dependency-graph"] = func(context.Context, sdk.FactRequest) (any, error) {
		return nil, &sdk.ToolError{Tool: "sbt", Reason: sdk.ErrToolNotFound}
	}
	e := mustEngine(t, "", ecosystem, findingAnalyzer("vulns", sdk.SeverityHigh, "dependency-graph"))
	res, _ := e.Scan(context.Background())
	if res.Projects[0].Status != sdk.ProjectSkipped || !strings.Contains(res.Projects[0].Reason, "tool-not-found") || Failed(res) {
		t.Errorf("project: %+v", res.Projects[0])
	}
	if !hasWarning(res, "project-skipped") {
		t.Error("warning missing")
	}
}

func hasWarning(res *sdk.ScanResult, code string) bool {
	for _, w := range res.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestFaultIsolation(t *testing.T) {
	panicky := &sdktest.FakeAnalyzer{ID: "panicky", Func: func(context.Context, sdk.AnalyzeRequest) ([]sdk.Finding, error) { panic("kaboom") }}
	slow := &sdktest.FakeAnalyzer{ID: "slow", Func: func(ctx context.Context, _ sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		return nil, nil
	}}
	invalid := &sdktest.FakeAnalyzer{ID: "invalid", Func: func(context.Context, sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		return []sdk.Finding{{Rule: "x"}}, nil
	}}
	e := mustEngine(t, "pluginTimeout: 50ms\nconcurrency: 2\n", eco(), panicky, slow, invalid, findingAnalyzer("good", sdk.SeverityLow))
	res, _ := e.Scan(context.Background())
	statuses := map[string]string{}
	for _, r := range res.PluginRuns {
		statuses[r.Plugin] += string(r.Status) + ":" + r.Reason + ";"
	}
	if !strings.Contains(statuses["panicky"], "plugin panicked: kaboom") || !strings.Contains(statuses["slow"], "timed out") {
		t.Errorf("statuses: %v", statuses)
	}
	if len(res.Findings) != 2 || !hasWarning(res, "invalid-finding") || !hasWarning(res, "plugin-failed") || !Failed(res) {
		t.Errorf("findings %d, warnings %+v", len(res.Findings), res.Warnings)
	}
}

func TestPluginConfiguration(t *testing.T) {
	a := &sdktest.FakeAnalyzer{ID: "fake-analyzer", Settings: &sdktest.FakeSettings{}}
	e := mustEngine(t, "plugins:\n  fake-analyzer:\n    threshold: 3\n    timeout: 5s\n  maven:\n    enabled: false\n", eco(), a)
	if a.Settings.Threshold != 3 || a.Settings.Timeout.Std() != 5*time.Second {
		t.Errorf("settings: %+v", a.Settings)
	}
	if len(e.Enabled(sdk.KindEcosystem)) != 0 || len(e.Plugins()) != 2 || e.Plugins()[1].Enabled {
		t.Errorf("enabled: %+v", e.Plugins())
	}
	res, _ := e.Scan(context.Background())
	if len(res.Projects) != 0 {
		t.Error("disabled ecosystem must not detect")
	}

	cases := map[string]string{
		"plugins:\n  fake-analyzer:\n    timeout: fast\n":   `deepward.yaml:3: plugins.fake-analyzer.timeout: invalid duration "fast"`,
		"plugins:\n  gradle:\n    enabled: true\n":          `plugins.gradle: unknown plugin "gradle"`,
		"plugins:\n  fake-analyzer:\n    threshold: -1\n":   "plugins.fake-analyzer: threshold must be >= 0",
		"plugins:\n  fake-analyzer:\n    bogus: 1\n":        `plugins.fake-analyzer.bogus: unknown key "bogus"`,
		"plugins:\n  maven:\n    color: red\n":              `plugins.maven.color: plugin maven has no settings besides "enabled"`,
		"plugins:\n  maven: [1]\n":                          "plugins.maven: expected a mapping",
		"plugins:\n  maven:\n    enabled: maybe\n":          "plugins.maven.enabled",
		"ecosystems:\n  gradle: {}\n":                       `ecosystems.gradle: unknown ecosystem plugin "gradle"`,
		"ecosystems:\n  maven:\n    addons:\n      x: {}\n": "ecosystems.maven.addons.x.kind is required",
		"failOn: high\nignore:\n  - id: CVE-1\n":            "ignore[0].reason is required",
	}
	for cfg, want := range cases {
		a := &sdktest.FakeAnalyzer{ID: "fake-analyzer", Settings: &sdktest.FakeSettings{}}
		_, err := newEngine(t, cfg, eco(), a)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config %q: err = %v, want %q", cfg, err, want)
		}
	}
	// null section keeps defaults
	if _, err := newEngine(t, "plugins:\n  maven:\n", eco()); err != nil {
		t.Errorf("null section: %v", err)
	}
}

type optionalPlugin struct{ sdktest.FakeAnalyzer }

func (o *optionalPlugin) Info() sdk.Info {
	i := o.FakeAnalyzer.Info()
	i.DisabledByDefault = true
	return i
}

func TestDisabledByDefault(t *testing.T) {
	e := mustEngine(t, "", &optionalPlugin{sdktest.FakeAnalyzer{ID: "ghsa"}})
	if len(e.Enabled(sdk.KindAnalyzer)) != 0 {
		t.Error("disabled by default")
	}
	e = mustEngine(t, "plugins:\n  ghsa:\n    enabled: true\n", &optionalPlugin{sdktest.FakeAnalyzer{ID: "ghsa"}})
	if len(e.Enabled(sdk.KindAnalyzer)) != 1 {
		t.Error("explicitly enabled")
	}
}

func TestConfigValidate(t *testing.T) {
	limit := -1
	c := Config{FailOn: "bogus", Ignore: []IgnoreRule{{}}, Concurrency: -1, Fix: FixConfig{Limit: &limit, Groups: []GroupRule{{}}},
		Ecosystems: map[string]EcosystemConfig{"maven": {Setup: []SetupStep{{}}}}, Projects: map[string]ProjectConfig{"api": {Setup: []SetupStep{{}}}}}
	err := c.Validate()
	for _, want := range []string{"failOn", "ignore[0].id", "ignore[0].reason", "ecosystems.maven.setup[0].tool", "projects.api.setup[0].tool", "concurrency", "fix.limit", "fix.groups[0]"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s in %v", want, err)
		}
	}
	if _, err := New(context.Background(), Options{Config: c}); err == nil {
		t.Error("New must validate config")
	}
}

type initPlugin struct {
	sdktest.FakeAnalyzer
	host sdk.Host
	err  error
}

func (p *initPlugin) Init(_ context.Context, h sdk.Host) error { p.host = h; return p.err }

func TestInitHostAndAddons(t *testing.T) {
	ecosystem := eco()
	ecosystem.AddonSpecs = []sdk.AddonSpec{
		{Name: "dependency-plugin", Kind: sdk.AddonMavenGoal, Default: sdk.Addon{Coordinates: "org.apache.maven.plugins:maven-dependency-plugin", Version: "3.8.1", Goal: "tree"}},
		{Name: "graph-dump", Kind: sdk.AddonSbtPluginFile, Default: sdk.Addon{File: "embedded", Builtin: true}},
	}
	p := &initPlugin{FakeAnalyzer: sdktest.FakeAnalyzer{ID: "with-init"}}
	reg := sdk.NewRegistry()
	_ = reg.Register(ecosystem)
	_ = reg.Register(p)
	cfg := parseConfig(t, `offline: true
ecosystems:
  maven:
    addons:
      dependency-plugin: {version: 3.6.1}
      graph-dump: {file: ci/x.sbt, coordinates: c, goal: g}
      corp: {kind: sbt-plugin-file, file: ci/corp.sbt}
`)
	e, err := New(context.Background(), Options{Registry: reg, Config: cfg, Workspace: workspace(), CacheDir: "/cache", NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	h := p.host
	if h.Settings().CacheDir != "/cache/with-init" || !h.Settings().Offline || !h.Settings().NoCache || h.Clock() == nil || h.Logger() == nil || h.Tools() == nil {
		t.Errorf("settings: %+v", h.Settings())
	}
	if len(h.Plugins(sdk.KindEcosystem)) != 1 {
		t.Error("host plugins")
	}
	addons := e.host("maven").Addons()
	if len(addons) != 3 || addons[0].Version != "3.6.1" || addons[0].Goal != "tree" || addons[1].File != "ci/x.sbt" || addons[1].Builtin ||
		addons[2].Name != "corp" || addons[2].Kind != sdk.AddonSbtPluginFile {
		t.Errorf("addons: %+v", addons)
	}
	if len(e.host("with-init").Addons()) != 0 {
		t.Error("non-provider has no addons")
	}
	if _, err := e.tools.Require(context.Background(), sdk.ToolRequirement{Name: "mvn"}, nil); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Error("noTools")
	}
	if e.Config().Offline != true || e.Workspace() == nil {
		t.Error("accessors")
	}

	failing := &initPlugin{FakeAnalyzer: sdktest.FakeAnalyzer{ID: "bad-init"}, err: errors.New("no creds")}
	reg2 := sdk.NewRegistry()
	_ = reg2.Register(failing)
	if _, err := New(context.Background(), Options{Registry: reg2}); err == nil || !strings.Contains(err.Error(), "bad-init: init: no creds") {
		t.Errorf("init error: %v", err)
	}
	if _, err := New(context.Background(), Options{}); err != nil {
		t.Error("empty engine")
	}
}

func TestIgnoreRulesAndGate(t *testing.T) {
	vuln := &sdktest.FakeAnalyzer{ID: "vulns", Func: func(context.Context, sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		mk := func(rule string, sev sdk.Severity, purl string, aliases ...string) sdk.Finding {
			return sdk.Finding{Rule: rule, Kind: sdk.KindVulnerability, Severity: sev, Message: rule, Aliases: aliases,
				Locations: []sdk.Location{{Package: &sdk.PackageLocation{PURL: purl}}}}
		}
		return []sdk.Finding{
			mk("GHSA-x", sdk.SeverityHigh, "pkg:maven/org.yaml/snakeyaml@1.33", "CVE-2022-1471"),
			mk("CVE-2021-44228", sdk.SeverityCritical, "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"),
			mk("CVE-2020-1", sdk.SeverityMedium, "pkg:maven/a/b@1"),
			mk("CVE-2020-2", sdk.SeverityHigh, "pkg:maven/c/d@1"),
		}, nil
	}}
	cfg := `failOn: high
ignore:
  - id: CVE-2022-1471
    purl: pkg:maven/org.yaml/*
    reason: SnakeYAML Constructor not used
    expires: 2027-01-01
  - id: CVE-2021-44228
    reason: expired rule
    expires: 2026-10-01
  - id: CVE-2020-1
    analyzer: other
    reason: different analyzer
  - id: CVE-2020-2
    purl: pkg:maven/x/y
    reason: different package
`
	e := mustEngine(t, cfg, &sdktest.FakeEcosystem{ID: "maven", Marker: "README.md"}, vuln)
	res, _ := e.Scan(context.Background())
	var suppressed []string
	for _, f := range res.Findings {
		if f.Suppression != nil {
			suppressed = append(suppressed, f.Rule+":"+f.Suppression.Reason+":"+f.Suppression.Expires)
		}
	}
	if len(suppressed) != 1 || suppressed[0] != "GHSA-x:SnakeYAML Constructor not used:2027-01-01" {
		t.Errorf("suppressed: %v", suppressed)
	}
	if !hasWarning(res, "expired-ignore-rule") {
		t.Error("expired rule warning")
	}
	if !res.Gate.Failed || res.Gate.Count != 2 || res.Gate.Threshold != sdk.SeverityHigh {
		t.Errorf("gate: %+v", res.Gate)
	}
	if g := evaluateGate(res.Findings, ""); g.Failed {
		t.Error("no threshold, no failure")
	}
	exact := IgnoreRule{ID: "CVE-2020-2", PURL: "pkg:maven/c/d@*"}
	f := res.Findings[len(res.Findings)-1]
	for _, x := range res.Findings {
		if x.Rule == "CVE-2020-2" {
			f = x
		}
	}
	if !matches(exact, &f) {
		t.Error("exact purl match with @* suffix")
	}
	noPkg := sdk.Finding{Rule: "R", Locations: []sdk.Location{{File: &sdk.FileLocation{Path: "a"}}}}
	if matches(IgnoreRule{ID: "R", PURL: "pkg:maven/*"}, &noPkg) {
		t.Error("purl rule must not match file-only findings")
	}
}

func TestSetupSteps(t *testing.T) {
	restore := &sdktest.FakeTool{ToolName: "dotnet"}
	failing := &sdktest.FakeTool{ToolName: "mvn", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{ExitCode: 1, Tail: []string{"BUILD FAILURE"}}, &sdk.ExitError{Tool: "mvn", ExitCode: 1}
	}}
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"dotnet": restore, "mvn": failing}}
	reg := sdk.NewRegistry()
	_ = reg.Register(eco())
	_ = reg.Register(findingAnalyzer("vulns", sdk.SeverityHigh))
	cfg := parseConfig(t, `ecosystems:
  maven:
    setup:
      - tool: dotnet
        args: [tool, restore]
projects:
  web:
    setup:
      - {tool: mvn, args: [install], dir: sub}
  api:
    setup:
      - {tool: missing}
`)
	e, err := New(context.Background(), Options{Registry: reg, Config: cfg, Workspace: workspace(), Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := e.Scan(context.Background())
	if len(restore.Invocations) != 2 || restore.Invocations[0].Args[1] != "restore" {
		t.Errorf("setup invocations: %+v", restore.Invocations)
	}
	if !strings.HasSuffix(failing.Invocations[0].Dir, "sub") {
		t.Errorf("setup dir: %s", failing.Invocations[0].Dir)
	}
	api, web := res.Projects[0], res.Projects[1]
	if api.Status != sdk.ProjectFailed || !strings.Contains(api.Reason, "tool-not-found") {
		t.Errorf("api: %+v", api)
	}
	if web.Status != sdk.ProjectFailed || web.Output[0] != "BUILD FAILURE" || !hasWarning(res, "setup-failed") {
		t.Errorf("web: %+v", web)
	}
	if len(res.Findings) != 0 {
		t.Error("analyzers must be skipped for failed setup")
	}
}

func TestStandaloneProducerWinsAndCycles(t *testing.T) {
	ecosystem := eco()
	sbom := &sdktest.FakeFactProducer{ID: "sbom-import", Names: []string{"dependency-graph"},
		Applies: func(p sdk.Project) bool { return p.Dir == "api" },
		Func:    func(context.Context, sdk.FactRequest) (any, error) { return &graphFact{n: 9}, nil }}
	cyclic := &sdktest.FakeFactProducer{ID: "cyclic", Names: []string{"a"}, Func: func(ctx context.Context, req sdk.FactRequest) (any, error) {
		return req.Facts.Fact(ctx, "a")
	}}
	nilProducer := &sdktest.FakeFactProducer{ID: "nil-producer", Names: []string{"nil"}, Func: func(context.Context, sdk.FactRequest) (any, error) { return nil, nil }}
	needs := &sdktest.FakeAnalyzer{ID: "needs", Needs: []string{"dependency-graph", "a"}}
	needsNil := &sdktest.FakeAnalyzer{ID: "needs-nil", Needs: []string{"nil", "none"}}
	e := mustEngine(t, "", ecosystem, sbom, cyclic, nilProducer, needs, needsNil, &sdktest.FakeAnalyzer{ID: "ws", Scoped: sdk.ScopeWorkspace,
		Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
			if len(req.Projects) != 2 || req.Project != nil {
				return nil, errors.New("workspace analyzer got wrong request")
			}
			return []sdk.Finding{sdktest.SimpleFinding("", "ws-rule", sdk.SeverityLow, "")}, nil
		}})
	res, _ := e.Scan(context.Background())
	if sbom.Calls.Load() != 1 || ecosystem.Calls.Load() != 1 {
		t.Errorf("sbom %d, ecosystem %d", sbom.Calls.Load(), ecosystem.Calls.Load())
	}
	if res.Projects[0].Facts["dependency-graph"].(map[string]int)["nodes"] != 9 {
		t.Error("sbom graph must win for api")
	}
	var cycleSeen, nilSeen bool
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "dependency cycle") {
			cycleSeen = true
		}
		if strings.Contains(w.Message, "returned no value") {
			nilSeen = true
		}
	}
	if !cycleSeen || !nilSeen {
		t.Errorf("projects: %+v", res.Projects)
	}
	if len(res.Findings) != 1 || res.Findings[0].Project != "" {
		t.Errorf("workspace finding: %+v", res.Findings)
	}
}

type dataPlugin struct{ sdktest.FakeAnalyzer }

func (*dataPlugin) DataSources() []sdk.DataSource {
	return []sdk.DataSource{{Name: "osv", License: "CC-BY-4.0"}}
}

func (*dataPlugin) Warnings() []sdk.Warning {
	return []sdk.Warning{{Code: "registry-unavailable", Message: "nexus: HTTP 502"}}
}

type failingEco struct{ sdktest.FakeEcosystem }

func (*failingEco) Detect(context.Context, *sdk.Workspace) ([]sdk.Project, error) {
	return nil, errors.New("cannot read")
}

func TestDetectFailuresDataSourcesAndFilters(t *testing.T) {
	dup := &sdktest.FakeEcosystem{ID: "dup", Marker: "pom.xml"}
	e := mustEngine(t, "", eco(), dup, &failingEco{sdktest.FakeEcosystem{ID: "broken"}}, &dataPlugin{sdktest.FakeAnalyzer{ID: "data"}},
		findingAnalyzer("vulns", sdk.SeverityHigh))
	res, _ := e.ScanWorkspace(context.Background(), workspace(), ScanFilter{Projects: []string{"maven:api"}, Analyzers: []string{"vulns"}})
	var pluginWarning *sdk.Warning
	for i := range res.Warnings {
		if res.Warnings[i].Code == "registry-unavailable" {
			pluginWarning = &res.Warnings[i]
		}
	}
	if pluginWarning == nil || pluginWarning.Plugin != "data" {
		t.Errorf("plugin warnings: %+v", res.Warnings)
	}
	if e.Plugin("data") == nil || e.Plugin("nope") != nil {
		t.Error("Plugin lookup")
	}
	if len(res.Projects) != 1 || len(res.Findings) != 1 || len(res.DataSources) != 1 || !hasWarning(res, "plugin-failed") {
		t.Errorf("result: projects=%d findings=%d sources=%d warnings=%+v", len(res.Projects), len(res.Findings), len(res.DataSources), res.Warnings)
	}
	noWS, _ := New(context.Background(), Options{})
	if _, err := noWS.Scan(context.Background()); err == nil {
		t.Error("scan without workspace")
	}
}

func TestGuard(t *testing.T) {
	if err := guard(context.Background(), 0, func(context.Context) error { return nil }); err != nil {
		t.Error("default timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := guard(ctx, time.Second, func(ctx context.Context) error { <-ctx.Done(); time.Sleep(5 * time.Millisecond); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	var pe *PanicError
	if err := guard(context.Background(), time.Second, func(context.Context) error { panic(1) }); !errors.As(err, &pe) || pe.Stack == "" {
		t.Errorf("panic: %v", err)
	}
}

func TestFactWaitersAndCancellation(t *testing.T) {
	release := make(chan struct{})
	slowEco := eco()
	slowEco.Facts["dependency-graph"] = func(context.Context, sdk.FactRequest) (any, error) {
		<-release
		return &graphFact{}, nil
	}
	e := mustEngine(t, "", slowEco)
	runs := &runLog{}
	pf := newProjectFacts(e, workspace(), sdk.Project{ID: "maven:api", Ecosystem: "maven", Dir: "api"}, runs)
	go func() { _, _ = pf.Fact(context.Background(), "dependency-graph") }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pf.Fact(ctx, "dependency-graph"); !errors.Is(err, sdk.ErrFactUnavailable) {
		t.Errorf("waiter cancel: %v", err)
	}
	if len(pf.snapshot()) != 0 {
		t.Error("unfinished entries are not in snapshot")
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	if v, err := pf.Fact(context.Background(), "dependency-graph"); err != nil || v == nil {
		t.Errorf("after release: %v %v", v, err)
	}
}
