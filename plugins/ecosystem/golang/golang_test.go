package golang

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func plugin(t *testing.T, tool *sdktest.FakeTool) *Plugin {
	t.Helper()
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	if tool != nil {
		tools.Tools["go"] = tool
	}
	p := New()
	if err := p.Init(context.Background(), &sdktest.FakeHost{ToolSet: tools, Run: sdk.RunSettings{NoCache: true}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func workspace(t *testing.T) (*sdk.Workspace, sdk.Project) {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"go.mod", "go.sum"} {
		b, _ := os.ReadFile(filepath.Join("testdata", "goapp", f))
		_ = os.WriteFile(filepath.Join(root, f), b, 0o644)
	}
	ws := sdk.NewWorkspace(root, nil)
	projects, err := New().Detect(context.Background(), ws)
	if err != nil || len(projects) != 1 {
		t.Fatalf("detect: %+v %v", projects, err)
	}
	return ws, projects[0]
}

// goTool replays the recorded outputs and rewrites go.mod/go.sum on `go get`.
func goTool(t *testing.T) *sdktest.FakeTool {
	list, _ := os.ReadFile("testdata/list.json")
	graph, _ := os.ReadFile("testdata/graph.txt")
	return &sdktest.FakeTool{ToolName: "go", ToolVersion: "1.26.3", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		switch spec.Args[0] {
		case "list":
			if !strings.Contains(strings.Join(spec.Args, " "), "-deps") {
				return sdk.RunResult{Stdout: list}, nil
			}
			// yaml.v2 is only imported on windows, check.v1 only by tests.
			out := "example.com/goapp\ngolang.org/x/text\n"
			if spec.Env["GOOS"] == "windows" {
				out += "gopkg.in/yaml.v2\n"
			}
			if strings.Contains(strings.Join(spec.Args, " "), "-test") {
				out += "gopkg.in/check.v1\n"
			}
			return sdk.RunResult{Stdout: []byte(out)}, nil
		case "mod":
			return sdk.RunResult{Stdout: graph}, nil
		case "get":
			for _, f := range []string{"go.mod", "go.sum"} {
				p := filepath.Join(spec.Dir, f)
				b, _ := os.ReadFile(p)
				_ = os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "golang.org/x/text v0.3.7", "golang.org/x/text v0.3.8")), 0o644)
			}
		}
		return sdk.RunResult{}, nil
	}}
}

func TestDetect(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"go.mod":                  {Data: []byte("module example.com/root\n")},
		"tools/go.mod":            {Data: []byte("module example.com/tools\n")},
		"tools/go.sum":            {Data: []byte("")},
		"vendor/x/go.mod":         {Data: []byte("module x\n")},
		"pkg/testdata/m/go.mod":   {Data: []byte("module y\n")},
		"_old/go.mod":             {Data: []byte("module z\n")},
		".cache/go.mod":           {Data: []byte("module w\n")},
		"node_modules/q/go.mod":   {Data: []byte("module q\n")},
		"internal/notgo.mod.json": {Data: []byte("")},
	}}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, _ := p.Detect(context.Background(), ws)
	if len(projects) != 2 || projects[0].ID != "go:." || projects[1].ID != "go:tools" {
		t.Fatalf("projects: %+v", projects)
	}
	if strings.Join(projects[1].Manifests, ",") != "tools/go.mod,tools/go.sum" || projects[1].Attributes["module"] != "example.com/tools" {
		t.Errorf("tools: %+v", projects[1])
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
}

func TestProduce(t *testing.T) {
	tool := goTool(t)
	p := plugin(t, tool)
	ws, project := workspace(t)
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws}
	g := sdktest.TestFactProducer(t, p, req).(*deps.Graph)
	if len(g.Packages()) != 4 || g.Root != "project:go:." {
		t.Errorf("graph: %d", len(g.Packages()))
	}
	inv := tool.Invocations[0]
	if strings.Join(inv.Args, " ") != "list -m -json all" || inv.Env["GOFLAGS"] != "-mod=readonly" || inv.Env["GOWORK"] != "off" || inv.Dir != ws.Root {
		t.Errorf("invocation: %+v", inv)
	}
	fail := func(failOn string, out []byte) error {
		ft := &sdktest.FakeTool{ToolName: "go", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
			if spec.Args[0] == failOn {
				return sdk.RunResult{}, &sdk.ExitError{Tool: "go", ExitCode: 1}
			}
			return sdk.RunResult{Stdout: out}, nil
		}}
		_, err := plugin(t, ft).Produce(context.Background(), req)
		return err
	}
	if err := fail("list", nil); err == nil || !strings.Contains(err.Error(), "go list") {
		t.Errorf("list failure: %v", err)
	}
	if err := fail("mod", []byte(`{"Path":"m","Main":true}`)); err == nil || !strings.Contains(err.Error(), "go mod graph") {
		t.Errorf("graph failure: %v", err)
	}
	if err := fail("none", []byte(`{`)); err == nil {
		t.Error("bad list output")
	}
	if _, err := plugin(t, nil).Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no go: %v", err)
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "x"}); err == nil {
		t.Error("unknown fact")
	}
}

func TestVersions(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/a/golang.org/x/text/@v/list":
			_, _ = w.Write([]byte("v0.3.8\nv0.3.7\n\nv0.4.0\n"))
		case "/a/github.com/!burnt!sushi/toml/@v/list":
			w.WriteHeader(http.StatusGone)
		case "/b/github.com/!burnt!sushi/toml/@v/list":
			_, _ = w.Write([]byte("v1.6.0\n"))
		case "/a/down.example/m/@v/list", "/b/down.example/m/@v/list":
			w.WriteHeader(http.StatusBadGateway)
		case "/a/teapot.example/m/@v/list":
			w.WriteHeader(http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	env := map[string]string{"GOPROXY": srv.URL + "/a," + srv.URL + "/b,direct", "GOPRIVATE": "git.acme.example"}
	p := New()
	p.Getenv = func(k string) string { return env[k] }
	_ = p.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{NoCache: true}})
	p.http.Retries, p.http.Backoff = 1, time.Millisecond
	project := sdk.Project{ID: "go:."}
	if vs, err := p.Versions(context.Background(), project, "pkg:golang/golang.org/x/text@v0.3.7"); err != nil || strings.Join(vs, ",") != "v0.3.7,v0.3.8,v0.4.0" {
		t.Errorf("text: %v %v", vs, err)
	}
	if vs, err := p.Versions(context.Background(), project, "pkg:golang/github.com/BurntSushi/toml"); err != nil || strings.Join(vs, ",") != "v1.6.0" {
		t.Errorf("410 falls through: %v %v", vs, err)
	}
	if _, err := p.Versions(context.Background(), project, "pkg:golang/missing.example/m"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("missing: %v", err)
	}
	hits = nil
	if _, err := p.Versions(context.Background(), project, "pkg:golang/git.acme.example/lib"); !errors.Is(err, deps.ErrPackageNotFound) || len(hits) != 0 {
		t.Errorf("private module must not reach a proxy: %v %v", err, hits)
	}
	if _, err := p.Versions(context.Background(), project, "pkg:golang/down.example/m"); err == nil || errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("comma stops on errors: %v", err)
	}
	env["GOPROXY"] = srv.URL + "/a|" + srv.URL + "/b"
	if _, err := p.Versions(context.Background(), project, "pkg:golang/down.example/m"); err == nil || !strings.Contains(err.Error(), "/b") {
		t.Errorf("pipe falls through on errors: %v", err)
	}
	if _, err := p.Versions(context.Background(), project, "pkg:golang/teapot.example/m"); err == nil {
		t.Error("unexpected status")
	}
	p.cfg = Config{Proxy: "off", Private: "none.example"}
	if _, err := p.Versions(context.Background(), project, "pkg:golang/golang.org/x/text"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("GOPROXY=off: %v", err)
	}
	p.cfg = Config{Proxy: srv.URL + "/a"}
	if _, err := p.Versions(context.Background(), project, "pkg:golang/bad%20path/x"); err == nil {
		t.Error("invalid module path")
	}
	if _, err := p.Versions(context.Background(), project, "nope"); err == nil {
		t.Error("bad purl")
	}
	if _, err := New().Versions(context.Background(), project, "pkg:golang/x/y"); err == nil {
		t.Error("not initialized")
	}
	if New().getenv("PATH") == "" {
		t.Error("getenv")
	}
}

func TestRealize(t *testing.T) {
	tool := goTool(t)
	p := plugin(t, tool)
	ws, project := workspace(t)
	res, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws,
		Changes: []sdk.PackageChange{{PURL: "pkg:golang/golang.org/x/text", From: "v0.3.7", To: "0.3.8"}, {PURL: "bad", To: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edits) != 2 || res.Edits[0].Path != "go.mod" || res.Edits[1].Path != "go.sum" || !strings.Contains(res.Edits[0].Replacement, "golang.org/x/text v0.3.8") || len(res.Manual) != 1 {
		t.Fatalf("result: %+v", res)
	}
	get := tool.Invocations[0]
	if strings.Join(get.Args, " ") != "get golang.org/x/text@v0.3.8" || get.Env["GOFLAGS"] != "-mod=mod" {
		t.Errorf("go get: %+v", get)
	}
	if b, _ := os.ReadFile(filepath.Join(ws.Root, "go.mod")); strings.Contains(string(b), "v0.3.8") {
		t.Error("workspace must be restored")
	}
	// Replaced modules and vendoring are manual.
	gomod := filepath.Join(ws.Root, "go.mod")
	orig, _ := os.ReadFile(gomod)
	_ = os.WriteFile(gomod, append(append([]byte{}, orig...), []byte("\nreplace gopkg.in/yaml.v2 => ../yaml\n")...), 0o644)
	res, _ = p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:golang/gopkg.in/yaml.v2", To: "v2.2.8"}}})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Reason, "replaced") {
		t.Errorf("replaced: %+v", res)
	}
	_ = os.WriteFile(gomod, orig, 0o644)
	_ = os.MkdirAll(filepath.Join(ws.Root, "vendor"), 0o755)
	_ = os.WriteFile(filepath.Join(ws.Root, "vendor", "modules.txt"), nil, 0o644)
	res, _ = p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:golang/gopkg.in/yaml.v2", To: "v2.2.8"}}})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Suggestion, "go mod vendor") {
		t.Errorf("vendored: %+v", res)
	}
	_ = os.RemoveAll(filepath.Join(ws.Root, "vendor"))
	change := sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:golang/gopkg.in/yaml.v2", To: "v2.2.8"}}}
	failing := &sdktest.FakeTool{ToolName: "go", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "go", ExitCode: 1, Tail: append(make([]string, 11), "no matching versions")}
	}}
	if _, err := plugin(t, failing).Realize(context.Background(), change); err == nil || !strings.Contains(err.Error(), "no matching versions") {
		t.Errorf("go get failure: %v", err)
	}
	if _, err := plugin(t, nil).Realize(context.Background(), change); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no go: %v", err)
	}
	if _, err := New().Realize(context.Background(), change); err == nil {
		t.Error("not initialized")
	}
	if tailError(errors.New("x")).Error() != "x" {
		t.Error("plain error")
	}
	_ = os.WriteFile(gomod, []byte("module ("), 0o644)
	if _, err := p.Realize(context.Background(), change); err == nil {
		t.Error("broken go.mod")
	}
	_ = os.Remove(gomod)
	if _, err := p.Realize(context.Background(), change); err == nil {
		t.Error("missing go.mod")
	}
}

func TestPluginContract(t *testing.T) {
	sdktest.TestPlugin(t, New())
	p := New()
	if len(p.ToolRequirements()) != 1 || !p.AppliesTo(sdk.Project{Ecosystem: ID}) || p.Produces()[0] != deps.FactDependencyGraph {
		t.Error("contracts")
	}
}

func TestProduceScopes(t *testing.T) {
	scopeOf := func(p *Plugin) map[string]string {
		t.Helper()
		ws, project := workspace(t)
		g, err := p.Produce(context.Background(), sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, n := range g.(*deps.Graph).Packages() {
			out[n.Name] = n.Scope
		}
		return out
	}
	tool := goTool(t)
	p := plugin(t, tool)
	got := scopeOf(p)
	if got["golang.org/x/text"] != "compile" || got["gopkg.in/yaml.v2"] != "compile" || got["gopkg.in/check.v1"] != "test" || got["golang.org/x/tools"] != "not-built" {
		t.Errorf("default platforms: %v", got)
	}
	deps0 := tool.Invocations[2]
	if strings.Join(deps0.Args, " ") != "list -e -deps -f {{with .Module}}{{.Path}}{{end}} ./..." || deps0.Env["GOOS"] != "linux" || deps0.Env["GOWORK"] != "off" {
		t.Errorf("deps invocation: %+v", deps0)
	}
	if len(tool.Invocations) != 2+2*len(DefaultPlatforms) {
		t.Errorf("invocations: %d", len(tool.Invocations))
	}
	// Without windows the windows-only module is not built; tags are passed.
	tool = goTool(t)
	p = plugin(t, tool)
	p.cfg.Platforms, p.cfg.Tags = []string{"linux/arm64"}, []string{"integration", "netgo"}
	if got := scopeOf(p); got["gopkg.in/yaml.v2"] != "not-built" {
		t.Errorf("linux only: %v", got)
	}
	if args := strings.Join(tool.Invocations[2].Args, " "); !strings.Contains(args, "-tags integration,netgo") || tool.Invocations[2].Env["GOARCH"] != "arm64" {
		t.Errorf("tags: %s", args)
	}
	// Listing fails: every module stays in scope compile.
	base := goTool(t)
	failing := &sdktest.FakeTool{ToolName: "go", RunFunc: func(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if strings.Contains(strings.Join(spec.Args, " "), "-deps") {
			return sdk.RunResult{}, &sdk.ExitError{Tool: "go", ExitCode: 1}
		}
		return base.RunFunc(ctx, spec)
	}}
	for name, scope := range scopeOf(plugin(t, failing)) {
		if scope != "compile" {
			t.Errorf("fallback: %s has %s", name, scope)
		}
	}
	if err := (&Config{Platforms: []string{"linux"}}).Validate(); err == nil {
		t.Error("platform without arch")
	}
	if err := (&Config{Platforms: []string{"linux/amd64/v3"}}).Validate(); err == nil {
		t.Error("platform with extra segment")
	}
	if err := (&Config{Platforms: DefaultPlatforms}).Validate(); err != nil {
		t.Error(err)
	}
}
