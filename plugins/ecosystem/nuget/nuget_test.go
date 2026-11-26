package nuget

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestDetect(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"Directory.Packages.props":      {Data: []byte("<Project/>")},
		"client/App.csproj":             {Data: []byte("<Project/>")},
		"client/packages.lock.json":     {Data: []byte("{}")},
		"client/NuGet.Config":           {Data: []byte("<configuration/>")},
		"tools/A.fsproj":                {Data: []byte("<Project/>")},
		"tools/B.vbproj":                {Data: []byte("<Project/>")},
		"tools/obj/project.assets.json": {Data: []byte("{}")},
		"App.sln":                       {Data: []byte("")},
		"other/packages.lock.json":      {Data: []byte("{}")},
	}}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, _ := p.Detect(context.Background(), ws)
	if len(projects) != 2 || projects[0].ID != "nuget:client" || projects[1].ID != "nuget:tools" {
		t.Fatalf("projects: %+v", projects)
	}
	if got := strings.Join(projects[0].Manifests, ","); got != "client/App.csproj,client/NuGet.Config,client/packages.lock.json,Directory.Packages.props" {
		t.Errorf("manifests: %s", got)
	}
	if projects[1].Attributes["projectFiles"] != "tools/A.fsproj,tools/B.vbproj" {
		t.Errorf("project files: %+v", projects[1].Attributes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
}

func fixture(t *testing.T) []byte {
	b, err := os.ReadFile("testdata/project.assets.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// dotnet returns a fake dotnet that writes assets next to the restored
// project file (or reports a custom path through msbuild).
func dotnet(t *testing.T, assets []byte, restoreErr error, customPath string) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "dotnet", ToolVersion: "8.0.404", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		switch spec.Args[0] {
		case "restore":
			if restoreErr != nil {
				return sdk.RunResult{Tail: []string{"error NU1101"}}, restoreErr
			}
			if customPath != "" {
				_ = os.MkdirAll(filepath.Dir(customPath), 0o755)
				return sdk.RunResult{}, os.WriteFile(customPath, assets, 0o600)
			}
			obj := filepath.Join(filepath.Dir(spec.Args[1]), "obj")
			_ = os.MkdirAll(obj, 0o755)
			return sdk.RunResult{}, os.WriteFile(filepath.Join(obj, "project.assets.json"), assets, 0o600)
		case "msbuild":
			return sdk.RunResult{Stdout: []byte(customPath + "\n")}, nil
		}
		return sdk.RunResult{}, nil
	}}
}

func setup(t *testing.T, tool *sdktest.FakeTool) (*Plugin, sdk.FactRequest) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "client"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "client", "App.csproj"), []byte("<Project/>"), 0o644)
	p := New()
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	if tool != nil {
		tools.Tools["dotnet"] = tool
	}
	_ = p.Init(context.Background(), &sdktest.FakeHost{ToolSet: tools})
	project := sdk.Project{ID: "nuget:client", Ecosystem: ID, Dir: "client", Manifests: []string{"client/App.csproj"},
		Attributes: map[string]string{"projectFiles": "client/App.csproj"}}
	return p, sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: sdk.NewWorkspace(root, nil)}
}

func TestProduce(t *testing.T) {
	tool := dotnet(t, fixture(t), nil, "")
	p, req := setup(t, tool)
	p.cfg.ConfigFile = "nuget.config"
	g := sdktest.TestFactProducer(t, p, req).(*deps.Graph)
	if len(g.Packages()) != 20 || g.Root != "project:nuget:client" {
		t.Errorf("graph: %d %s", len(g.Packages()), g.Root)
	}
	args := strings.Join(tool.Invocations[0].Args, " ")
	if !strings.Contains(args, "restore "+filepath.Join(req.Workspace.Root, "client", "App.csproj")) || !strings.Contains(args, "--configfile "+filepath.Join(req.Workspace.Root, "nuget.config")) {
		t.Errorf("args: %s", args)
	}
	custom := filepath.Join(t.TempDir(), "artifacts", "obj", "project.assets.json")
	p2, req2 := setup(t, dotnet(t, fixture(t), nil, custom))
	if _, err := p2.Produce(context.Background(), req2); err != nil {
		t.Errorf("custom assets path via msbuild: %v", err)
	}
	p2.cfg.ConfigFile = "/abs/nuget.config"
	_, _ = p2.Produce(context.Background(), req2)
	// Manifests fallback when projectFiles is missing.
	req2.Project.Attributes = nil
	if _, err := p2.Produce(context.Background(), req2); err != nil {
		t.Errorf("manifests fallback: %v", err)
	}
}

func TestProduceFailures(t *testing.T) {
	p, req := setup(t, dotnet(t, nil, &sdk.ExitError{Tool: "dotnet", ExitCode: 1, Tail: []string{"error NU1101"}}, ""))
	var xe *sdk.ExitError
	if _, err := p.Produce(context.Background(), req); !errors.As(err, &xe) {
		t.Errorf("restore failure: %v", err)
	}
	p, req = setup(t, nil)
	if _, err := p.Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("tool missing: %v", err)
	}
	p, req = setup(t, dotnet(t, []byte("{"), nil, ""))
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("bad assets")
	}
	noAssets := &sdktest.FakeTool{ToolName: "dotnet", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) { return sdk.RunResult{}, nil }}
	p, req = setup(t, noAssets)
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "project.assets.json not found") {
		t.Errorf("no assets: %v", err)
	}
	p, req = setup(t, dotnet(t, fixture(t), nil, filepath.Join(t.TempDir(), "missing-dir", "x.json")))
	p2 := *p
	_ = p2
	req.Project.Attributes = map[string]string{"projectFiles": "client/README.md"}
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no .NET project files") {
		t.Errorf("no project files: %v", err)
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "x"}); err == nil {
		t.Error("unknown fact")
	}
	unreadable := &sdktest.FakeTool{ToolName: "dotnet", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] == "msbuild" {
			return sdk.RunResult{Stdout: []byte("/nonexistent/project.assets.json")}, nil
		}
		return sdk.RunResult{}, nil
	}}
	p, req = setup(t, unreadable)
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "read /nonexistent") {
		t.Errorf("unreadable assets: %v", err)
	}
}

func feedServer(t *testing.T, failures *atomic.Int64) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failures != nil && failures.Load() > 0 {
			failures.Add(-1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v3/index.json":
			_, _ = w.Write([]byte(`{"version":"3.0.0","resources":[{"@id":"` + srv.URL + `/flat","@type":"PackageBaseAddress/3.0.0"}]}`))
		case "/noflat/index.json":
			_, _ = w.Write([]byte(`{"resources":[]}`))
		case "/badindex/index.json":
			_, _ = w.Write([]byte(`nope`))
		case "/flat/newtonsoft.json/index.json":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"versions":["12.0.3","13.0.1","13.0.4-beta1"]}`))
		case "/flat/garbage/index.json":
			_, _ = w.Write([]byte(`garbage`))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestFeeds(t *testing.T) {
	var failures atomic.Int64
	failures.Store(1)
	srv := feedServer(t, &failures)
	defer srv.Close()
	p := New()
	p.cfg = Config{Feeds: []Feed{{ID: "corp", URL: srv.URL + "/v3/index.json", UsernameEnv: "U", PasswordEnv: "P"}}}
	p.Getenv = func(k string) string { return map[string]string{"U": "ci", "P": "pw"}[k] }
	_ = p.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{CacheDir: t.TempDir()}})
	p.feeds.backoff = time.Millisecond
	vs, err := p.Versions(context.Background(), sdk.Project{}, "pkg:nuget/Newtonsoft.Json")
	if err != nil || strings.Join(vs, ",") != "12.0.3,13.0.1,13.0.4-beta1" {
		t.Errorf("versions: %v %v", vs, err)
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "pkg:nuget/Missing"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("not found: %v", err)
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "pkg:nuget/garbage"); err == nil {
		t.Error("garbage index")
	}
	token := New()
	token.cfg = Config{Feeds: []Feed{{URL: srv.URL + "/v3/index.json", TokenEnv: "T"}}}
	token.Getenv = func(string) string { return "tok" }
	_ = token.Init(context.Background(), &sdktest.FakeHost{})
	if _, err := token.Versions(context.Background(), sdk.Project{}, "pkg:nuget/newtonsoft.json"); err != nil {
		t.Errorf("token auth: %v", err)
	}
	anon := New()
	anon.cfg = Config{Feeds: []Feed{{URL: srv.URL + "/v3/index.json"}}}
	_ = anon.Init(context.Background(), &sdktest.FakeHost{})
	if _, err := anon.Versions(context.Background(), sdk.Project{}, "pkg:nuget/newtonsoft.json"); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("unauthorized: %v", err)
	}
	for _, path := range []string{"/noflat/index.json", "/badindex/index.json", "/missing/index.json"} {
		b := New()
		b.cfg = Config{Feeds: []Feed{{URL: srv.URL + path}}}
		_ = b.Init(context.Background(), &sdktest.FakeHost{})
		if _, err := b.Versions(context.Background(), sdk.Project{}, "pkg:nuget/x"); err == nil {
			t.Errorf("%s must fail", path)
		}
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	d := New()
	d.cfg = Config{Feeds: []Feed{{URL: down.URL + "/v3/index.json"}}}
	_ = d.Init(context.Background(), &sdktest.FakeHost{})
	d.feeds.backoff = time.Millisecond
	if _, err := d.Versions(context.Background(), sdk.Project{}, "pkg:nuget/x"); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("5xx: %v", err)
	}
	dead := New()
	dead.cfg = Config{Feeds: []Feed{{URL: "http://127.0.0.1:1/v3/index.json"}}}
	_ = dead.Init(context.Background(), &sdktest.FakeHost{})
	dead.feeds.backoff, dead.feeds.retries = time.Millisecond, 1
	if _, err := dead.Versions(context.Background(), sdk.Project{}, "pkg:nuget/x"); err == nil {
		t.Error("unreachable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dead.Versions(ctx, sdk.Project{}, "pkg:nuget/y"); err == nil {
		t.Error("cancelled")
	}
	badURL := New()
	badURL.cfg = Config{Feeds: []Feed{{URL: "http://[::1"}}}
	_ = badURL.Init(context.Background(), &sdktest.FakeHost{})
	if _, err := badURL.Versions(context.Background(), sdk.Project{}, "pkg:nuget/y"); err == nil {
		t.Error("bad url")
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "bad"); err == nil {
		t.Error("bad purl")
	}
	if _, err := New().Versions(context.Background(), sdk.Project{}, "pkg:nuget/x"); err == nil {
		t.Error("not initialized")
	}
	if (&Config{Feeds: []Feed{{}}}).Validate() == nil || (&Config{}).Validate() != nil {
		t.Error("validate")
	}
	if New().ConfigTarget() == nil || New().ToolRequirements()[0].Name != "dotnet" || !New().AppliesTo(sdk.Project{Ecosystem: "nuget"}) || New().Produces()[0] != deps.FactDependencyGraph {
		t.Error("metadata")
	}
	if New().getenv("PATH") == "" && os.Getenv("PATH") != "" {
		t.Error("getenv")
	}
}
