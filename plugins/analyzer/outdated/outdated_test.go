package outdated

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
	"github.com/deepward-oss/deepward/plugins/analyzer/outdated/domain"
)

const root = "project:sbt:."

type facts map[string]any

func (f facts) Fact(_ context.Context, name string) (any, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return nil, &sdk.FactError{Name: name}
}

func graph(t *testing.T) *deps.Graph {
	nodes := []deps.Node{
		{ID: "pkg:maven/org.typelevel/cats-core_2.13@2.9.0", Ecosystem: deps.Maven, Name: "org.typelevel:cats-core_2.13", Version: "2.9.0", Scope: "compile"},
		{ID: "pkg:maven/com.acme/billing-client@1.0", Ecosystem: deps.Maven, Name: "com.acme:billing-client", Version: "1.0", Scope: "compile"},
		{ID: "pkg:maven/org.scalatest/scalatest_2.13@3.2.0", Ecosystem: deps.Maven, Name: "org.scalatest:scalatest_2.13", Version: "3.2.0", Scope: "test"},
		{ID: "pkg:maven/a/transitive@1.0", Ecosystem: deps.Maven, Name: "a:transitive", Version: "1.0"},
		{ID: "pkg:maven/a/flaky@1.0", Ecosystem: deps.Maven, Name: "a:flaky", Version: "1.0"},
		{ID: "pkg:maven/a/unknown@1.0", Ecosystem: deps.Maven, Name: "a:unknown", Version: "1.0"},
		{ID: "pkg:maven/a/uptodate@9.0", Ecosystem: deps.Maven, Name: "a:uptodate", Version: "9.0"},
	}
	var edges []deps.Edge
	for _, n := range nodes {
		if n.Name != "a:transitive" {
			edges = append(edges, deps.Edge{From: root, To: n.ID})
		}
	}
	edges = append(edges, deps.Edge{From: nodes[0].ID, To: nodes[3].ID})
	g, err := deps.NewGraph(root, "t", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

type flakyEco struct{ *depstest.Ecosystem }

func (f flakyEco) Versions(ctx context.Context, p sdk.Project, pkg string) ([]string, error) {
	if strings.HasSuffix(pkg, "/flaky") {
		return nil, errors.New("nexus: HTTP 502 after 3 retries")
	}
	return f.Ecosystem.Versions(ctx, p, pkg)
}

func setup(t *testing.T, cfg Config) (*Analyzer, sdk.AnalyzeRequest) {
	t.Helper()
	eco := &depstest.Ecosystem{FakeEcosystem: sdktest.FakeEcosystem{ID: "sbt"}, Registry: &depstest.FakeRegistry{Versions: map[string][]string{
		"pkg:maven/org.typelevel/cats-core_2.13": {"2.9.0", "2.10.0", "2.13.0"},
		"pkg:maven/com.acme/billing-client":      {"1.0", "1.1"},
		"pkg:maven/org.scalatest/scalatest_2.13": {"3.2.19"},
		"pkg:maven/a/transitive":                 {"2.0"},
		"pkg:maven/a/uptodate":                   {"9.0"},
	}}}
	a := New()
	a.cfg = cfg
	_ = a.Init(context.Background(), &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{sdk.KindEcosystem: {flakyEco{eco}}}})
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{"build.sbt": {Data: []byte("name := \"app\"\nlibraryDependencies += \"org.typelevel\" %% \"cats-core\" % \"2.9.0\"\n")}}}
	req := sdk.AnalyzeRequest{Project: &sdk.Project{ID: "sbt:.", Ecosystem: "sbt", Dir: ".", Manifests: []string{"build.sbt"}}, Workspace: ws,
		Facts: facts{deps.FactDependencyGraph: graph(t)}}
	return a, req
}

func TestAnalyze(t *testing.T) {
	a, req := setup(t, Config{})
	found := sdktest.TestAnalyzer(t, a, req)
	byName := map[string]sdk.Finding{}
	for _, f := range found {
		byName[f.Properties["package"].(map[string]any)["name"].(string)] = f
	}
	if len(found) != 3 {
		t.Fatalf("findings: %d %+v", len(found), byName)
	}
	cats := byName["org.typelevel:cats-core_2.13"]
	if cats.Properties["minor"] != "2.13.0" || cats.Rule != "update-minor" || cats.Severity != sdk.SeverityNone || cats.Kind != sdk.KindOutdated {
		t.Errorf("cats: %+v", cats)
	}
	if cats.PrimaryFile().StartLine != 2 || cats.Remediations[0].Package.To != "2.13.0" {
		t.Errorf("location/remediation: %+v %+v", cats.PrimaryFile(), cats.Remediations)
	}
	if got := strings.Join(cats.Remediations[0].Affects, ","); got != "pkg:maven/a/transitive,pkg:maven/org.typelevel/cats-core_2.13" {
		t.Errorf("an update affects the package and what it brings: %s", got)
	}
	if got := strings.Join(byName["com.acme:billing-client"].Remediations[0].Affects, ","); got != "pkg:maven/com.acme/billing-client" {
		t.Errorf("an update of a leaf affects only the leaf: %s", got)
	}
	if _, ok := byName["a:transitive"]; ok {
		t.Error("only direct dependencies")
	}
	w := a.Warnings()
	if len(w) == 0 || w[0].Code != "registry-unavailable" || !strings.Contains(w[0].Message, "a:flaky") {
		t.Errorf("warnings: %+v", w)
	}
}

func TestRulesAndScopes(t *testing.T) {
	a, req := setup(t, Config{ExcludeScopes: []string{"test"}, Rules: []domain.Rule{
		{Match: "pkg:maven/com.acme/*", Ignore: true},
		{Match: "pkg:maven/org.typelevel/*", MaxUpdate: deps.UpdatePatch},
	}})
	found, err := a.Analyze(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("all filtered: %+v", found)
	}
	bad, req2 := setup(t, Config{Rules: []domain.Rule{{Match: "pkg:maven/*/*", Allow: "~1"}}})
	if _, err := bad.Analyze(context.Background(), req2); err == nil {
		t.Error("invalid range at analysis time")
	}
	req2.Facts = facts{}
	if _, err := New().Analyze(context.Background(), req2); err == nil {
		t.Error("missing graph")
	}
	noReg, req3 := setup(t, Config{})
	noReg.host = &sdktest.FakeHost{}
	if fs, err := noReg.Analyze(context.Background(), req3); fs != nil || err != nil {
		t.Error("no registry: nothing to report")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Analyze(ctx, req); err == nil {
		t.Error("cancelled")
	}
	if fs, _ := a.Analyze(context.Background(), sdk.AnalyzeRequest{}); fs != nil {
		t.Error("workspace request")
	}
	if New().registry("x") != nil {
		t.Error("no host")
	}
	noMan := req
	p := *req.Project
	p.Manifests = nil
	noMan.Project = &p
	if loc := manifestLocation(noMan, deps.Node{Name: "x"}); loc.Path != "." {
		t.Error("fallback")
	}
	if loc := manifestLocation(req, deps.Node{Name: "zzz:nothing"}); loc.Path != "build.sbt" || loc.StartLine != 0 {
		t.Error("not found in manifest")
	}
}

func TestConfigValidate(t *testing.T) {
	cases := map[string]Config{
		"match is required": {Rules: []domain.Rule{{}}},
		"maxUpdate must be": {Rules: []domain.Rule{{Match: "*", MaxUpdate: "huge"}}},
		"allow":             {Rules: []domain.Rule{{Match: "*", Allow: "~1"}}},
	}
	for want, c := range cases {
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if err := (&Config{Rules: []domain.Rule{{Match: "*", MaxUpdate: deps.UpdateMinor, Allow: "<3"}}}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestOutdatedInModules(t *testing.T) {
	a, req := setup(t, Config{})
	const cats = "pkg:maven/org.typelevel/cats-core_2.13@2.9.0"
	mod := func(id, name, manifest string) deps.Node {
		return deps.Node{ID: id, Name: name, Kind: deps.KindModule, Attributes: map[string]string{"manifest": manifest}}
	}
	g, err := deps.NewGraph(root, "t", []deps.Node{
		mod("module:web/build.sbt", "web", "web/build.sbt"), mod("module:core/build.sbt", "core", "core/build.sbt"),
		mod("module:app/build.sbt", "app", "app/build.sbt"),
		{ID: cats, Ecosystem: deps.Maven, Name: "org.typelevel:cats-core_2.13", Version: "2.9.0", Scope: "compile"},
	}, []deps.Edge{{From: root, To: "module:web/build.sbt"}, {From: root, To: "module:core/build.sbt"}, {From: root, To: "module:app/build.sbt"},
		{From: "module:web/build.sbt", To: cats}, {From: "module:core/build.sbt", To: cats}, {From: "module:app/build.sbt", To: "module:core/build.sbt"}})
	if err != nil {
		t.Fatal(err)
	}
	req.Facts = facts{deps.FactDependencyGraph: g}
	req.Workspace.FS = fstest.MapFS{"build.sbt": {Data: []byte("cats-core\n")},
		"core/build.sbt": {Data: []byte("name := \"core\"\nlibraryDependencies += \"org.typelevel\" %% \"cats-core\" % \"2.9.0\"\n")},
		"web/build.sbt":  {Data: []byte("cats-core\n")}}
	req.Project.Manifests = []string{"build.sbt", "core/build.sbt", "web/build.sbt"}
	found, err := a.Analyze(context.Background(), req)
	if err != nil || len(found) != 1 {
		t.Fatalf("one finding for the dependency: %v %+v", err, found)
	}
	loc := found[0].Locations[0]
	var names []string
	for _, m := range loc.Package.Modules {
		names = append(names, m.Name)
		if !m.Direct || m.Scope != "compile" {
			t.Errorf("declaring module: %+v", m)
		}
	}
	// app reaches cats only through core and does not declare it.
	if strings.Join(names, ",") != "core,web" {
		t.Errorf("declaring modules: %v", names)
	}
	if loc.File.Path != "core/build.sbt" || loc.File.StartLine != 2 {
		t.Errorf("located in the first declaring module: %+v", loc.File)
	}
}
