package sdktest

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// The fakes must pass the conformance kit themselves.

func ws() *sdk.Workspace {
	return &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"a/marker.txt":   {Data: []byte("x")},
		"b/c/marker.txt": {Data: []byte("x")},
		"other.txt":      {Data: []byte("x")},
	}}
}

func TestFakeEcosystemConforms(t *testing.T) {
	e := &FakeEcosystem{ID: "fake-eco", Marker: "marker.txt"}
	TestEcosystem(t, e, ws())
	projects, _ := e.Detect(context.Background(), ws())
	if len(projects) != 2 || projects[1].ID != "fake-eco:b/c" {
		t.Errorf("projects: %+v", projects)
	}
	if _, err := e.Detect(cancelled(), ws()); err == nil {
		t.Error("cancelled context")
	}
	e.Facts = map[string]func(context.Context, sdk.FactRequest) (any, error){
		"b": func(context.Context, sdk.FactRequest) (any, error) { return 1, nil },
		"a": func(context.Context, sdk.FactRequest) (any, error) { return 2, nil },
	}
	if strings.Join(e.Produces(), ",") != "a,b" || !e.AppliesTo(projects[0]) {
		t.Error("produces/applies")
	}
	TestFactProducer(t, e, sdk.FactRequest{Name: "a", Project: projects[0]})
	if _, err := e.Produce(context.Background(), sdk.FactRequest{Name: "zzz"}); err == nil {
		t.Error("unknown fact")
	}
	if _, err := e.Realize(context.Background(), sdk.RealizeRequest{}); err == nil {
		t.Error("realize without func")
	}
	e.RealizeFunc = func(context.Context, sdk.RealizeRequest) (sdk.RealizeResult, error) { return sdk.RealizeResult{}, nil }
	if _, err := e.Realize(context.Background(), sdk.RealizeRequest{}); err != nil {
		t.Error("realize")
	}
	if e.Addons() != nil || e.ToolDefinitions() != nil {
		t.Error("empty addons/tools")
	}
}

func TestFakeAnalyzerConforms(t *testing.T) {
	a := &FakeAnalyzer{ID: "fake-analyzer", Settings: &FakeSettings{Threshold: 1}, Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		if req.Project == nil {
			return nil, nil
		}
		return []sdk.Finding{SimpleFinding("fake-analyzer", "R1", sdk.SeverityHigh, req.Project.ID)}, nil
	}}
	got := TestAnalyzer(t, a, sdk.AnalyzeRequest{Project: &sdk.Project{ID: "x:."}})
	if len(got) != 1 || a.Scope() != sdk.ScopeProject {
		t.Errorf("findings: %v", got)
	}
	if (&FakeAnalyzer{ID: "x"}).ConfigTarget() == nil {
		t.Error("lazy settings")
	}
	if (&FakeSettings{Threshold: -1}).Validate() == nil {
		t.Error("validation")
	}
	empty := &FakeAnalyzer{ID: "empty", Scoped: sdk.ScopeWorkspace}
	if fs, err := empty.Analyze(context.Background(), sdk.AnalyzeRequest{}); fs != nil || err != nil || empty.Scope() != sdk.ScopeWorkspace {
		t.Error("nil func")
	}
}

func TestFakeFactProducerConforms(t *testing.T) {
	p := &FakeFactProducer{ID: "fake-facts", Names: []string{"x"}, Func: func(context.Context, sdk.FactRequest) (any, error) { return "v", nil }}
	if v := TestFactProducer(t, p, sdk.FactRequest{Name: "x", Project: sdk.Project{ID: "a:."}}); v != "v" {
		t.Error("value")
	}
	p.Applies = func(sdk.Project) bool { return false }
	if p.AppliesTo(sdk.Project{}) {
		t.Error("applies override")
	}
}

func TestFakeReporterConforms(t *testing.T) {
	TestReporter(t, &FakeReporter{ID: "fake-reporter", Name: "txt"}, &sdk.ScanResult{Findings: []sdk.Finding{{}}})
}

func TestFakeOutputFormatConforms(t *testing.T) {
	sample := []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"lint"}},"results":[{"ruleId":"r","level":"warning","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.go"},"region":{"startLine":3}}}]}]}]}`)
	res := TestOutputFormat(t, &FakeOutputFormat{ID: "fake-sarif", Format: "sarif"}, sample)
	if len(res.Findings) != 1 || res.Findings[0].Tool != "conformance" {
		t.Errorf("parsed: %+v", res.Findings)
	}
	fact := &FakeOutputFormat{ID: "fake-json", Format: "json", FactName: "thing"}
	res = TestOutputFormat(t, fact, []byte(`{"a":1}`))
	if res.Fact == nil || fact.Produces()[0] != "thing" {
		t.Error("fact parse")
	}
}

func TestFakeToolsAndResolver(t *testing.T) {
	r := &FakeToolResolver{ID: "fake-resolver", Commands: map[string][]string{"mvn": {"/usr/bin/mvn"}}}
	if c := TestToolResolver(t, r, sdk.ResolveRequest{Definition: sdk.ToolDefinition{Name: "mvn"}}); c == nil || c.Command[0] != "/usr/bin/mvn" {
		t.Errorf("candidate: %+v", c)
	}
	tool := &FakeTool{ToolName: "mvn", ToolVersion: "3.9.9"}
	tools := &FakeTools{Tools: map[string]*FakeTool{"mvn": tool}}
	got, err := tools.Require(context.Background(), sdk.ToolRequirement{Name: "mvn"}, nil)
	if err != nil || got.Version() != "3.9.9" || got.Resolver() != "fake" || got.Command()[0] != "mvn" || got.Name() != "mvn" {
		t.Fatal("require")
	}
	if _, err := got.Run(context.Background(), sdk.RunSpec{Args: []string{"-v"}}); err != nil || len(tool.Invocations) != 1 {
		t.Error("run")
	}
	tool.RunFunc = func(context.Context, sdk.RunSpec) (sdk.RunResult, error) { return sdk.RunResult{ExitCode: 3}, nil }
	if res, _ := got.Run(context.Background(), sdk.RunSpec{}); res.ExitCode != 3 {
		t.Error("run func")
	}
	if _, err := tools.Require(context.Background(), sdk.ToolRequirement{Name: "sbt"}, nil); err == nil {
		t.Error("missing tool")
	}
	h := &FakeHost{}
	if h.Tools() == nil || h.Logger() == nil || h.Clock() == nil || h.Plugins(sdk.KindAnalyzer) != nil || h.Addons() != nil || h.Settings().Offline {
		t.Error("host defaults")
	}
}

func TestFakePlatformConforms(t *testing.T) {
	p := &FakePlatform{ID: "fake-platform", Domain: "fake.example"}
	c := TestPlatform(t, p, sdk.PlatformConfig{Repo: "g/p", Token: "t"})
	if !p.Detect("https://fake.example/g/p.git") || p.Detect("https://other") {
		t.Error("detect")
	}
	if _, err := p.Connect(context.Background(), sdk.PlatformConfig{}); err == nil {
		t.Error("token required")
	}
	ctx := context.Background()
	if b, _ := c.DefaultBranch(ctx); b != "main" {
		t.Error("default branch")
	}
	if _, err := c.Commit(ctx, sdk.CommitRequest{Branch: "deepward/x", BaseBranch: "main", Files: []sdk.FileChange{{Path: "a", Content: []byte("1")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(ctx, sdk.CommitRequest{Branch: "deepward/x", BaseBranch: "main"}); err == nil {
		t.Error("existing branch without force")
	}
	if _, err := c.Commit(ctx, sdk.CommitRequest{Branch: "deepward/x", BaseBranch: "deepward/x", Force: true, Files: []sdk.FileChange{{Path: "a", Delete: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.ReadFile(ctx, "deepward/x", "a"); ok {
		t.Error("deleted file")
	}
	cr, _ := c.CreateChangeRequest(ctx, sdk.ChangeRequestSpec{Title: "t", SourceBranch: "deepward/x", TargetBranch: "main"})
	if list, _ := c.ListChangeRequests(ctx, "deepward/"); len(list) != 1 {
		t.Error("list")
	}
	if _, err := c.UpdateChangeRequest(ctx, cr.ID, sdk.ChangeRequestSpec{Title: "t2"}); err != nil {
		t.Error("update")
	}
	if _, err := c.UpdateChangeRequest(ctx, "nope", sdk.ChangeRequestSpec{}); err == nil {
		t.Error("update missing")
	}
	_ = c.Comment(ctx, cr.ID, "hi")
	if err := c.CloseChangeRequest(ctx, cr.ID); err != nil {
		t.Error("close")
	}
	if err := c.CloseChangeRequest(ctx, "nope"); err == nil {
		t.Error("close missing")
	}
	if list, _ := c.ListChangeRequests(ctx, "deepward/"); len(list) != 0 {
		t.Error("closed not listed")
	}
	commits, _ := c.BranchCommits(ctx, "deepward/x", "main")
	repo := p.Repo
	if len(commits) != 1 || !commits[0].IsBot || len(repo.Writes) != 6 || len(repo.Comments[cr.ID]) != 1 {
		t.Errorf("writes: %v", repo.Writes)
	}
}

// TestKitDetectsViolations runs the kit against broken plugins using a
// recording testing.T substitute.
func TestKitDetectsViolations(t *testing.T) {
	badFinding := func(context.Context, sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		return []sdk.Finding{{Analyzer: "other", Rule: "", Kind: "x", Message: "m"}}, nil
	}
	inner := &testing.T{}
	ran := runIsolated(func(tt *testing.T) {
		TestAnalyzer(tt, &FakeAnalyzer{ID: "Bad_ID", Func: badFinding}, sdk.AnalyzeRequest{})
	}, inner)
	if !ran {
		t.Error("kit must report failures for a broken analyzer")
	}
}

func runIsolated(fn func(*testing.T), _ *testing.T) (failed bool) {
	// testing.T cannot be constructed directly; use a sub-test whose
	// failure we observe via the parent's result.
	res := testing.RunTests(func(pat, str string) (bool, error) { return true, nil },
		[]testing.InternalTest{{Name: "isolated", F: fn}})
	return !res
}
