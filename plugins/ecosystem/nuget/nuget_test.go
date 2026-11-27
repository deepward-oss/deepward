package nuget

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func sln(paths ...string) []byte {
	out := "Microsoft Visual Studio Solution File, Format Version 12.00\r\n"
	for _, p := range paths {
		out += "Project(\"{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}\") = \"X\", \"" + p + "\", \"{1}\"\r\nEndProject\r\n"
	}
	return []byte(out)
}

func detect(t *testing.T, files fstest.MapFS) []sdk.Project {
	t.Helper()
	ws := &sdk.Workspace{Root: "/repo", FS: files}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, err := p.Detect(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	return projects
}

func TestDetectCPMRoot(t *testing.T) {
	projects := detect(t, fstest.MapFS{
		"Directory.Packages.props":                 {Data: []byte("<Project/>")},
		"src/Web/Web.csproj":                       {Data: []byte("<Project/>")},
		"src/Web/packages.lock.json":               {Data: []byte("{}")},
		"src/Core/Core.csproj":                     {Data: []byte("<Project/>")},
		"tests/Core.Tests/Core.Tests.csproj":       {Data: []byte("<Project/>")},
		"tests/Core.Tests/obj/project.assets.json": {Data: []byte("{}")},
		"App.sln":      {Data: sln("src\\Web\\Web.csproj", "src\\Core\\Core.csproj")},
		"All.slnx":     {Data: []byte("<Solution><Project Path=\"src/Core/Core.csproj\" /><Project Path=\"missing/M.csproj\" /></Solution>")},
		"NuGet.Config": {Data: []byte("<configuration/>")},
	})
	if len(projects) != 1 || projects[0].ID != "nuget:." {
		t.Fatalf("one CPM project: %+v", projects)
	}
	pr := projects[0]
	if got := pr.Attributes["projectFiles"]; got != "src/Core/Core.csproj,src/Web/Web.csproj,tests/Core.Tests/Core.Tests.csproj" {
		t.Errorf("project files: %s", got)
	}
	// App.sln lists only members; All.slnx lists a missing project.
	if got := pr.Attributes["solutions"]; got != "App.sln" {
		t.Errorf("solutions to restore: %q", got)
	}
	if got := strings.Join(pr.Manifests, ","); got != "src/Core/Core.csproj,src/Web/Web.csproj,tests/Core.Tests/Core.Tests.csproj,src/Web/packages.lock.json,Directory.Packages.props,NuGet.Config" {
		t.Errorf("manifests: %s", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Detect(ctx, &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{}}); err == nil {
		t.Error("cancelled")
	}
}

func TestDetectSolutions(t *testing.T) {
	projects := detect(t, fstest.MapFS{
		"App.sln":              {Data: sln("Web\\Web.csproj", "Core\\Core.csproj")},
		"Web/Web.csproj":       {Data: []byte("<Project/>")},
		"Core/Core.csproj":     {Data: []byte("<Project/>")},
		"tools/Tools.sln":      {Data: sln("Cli\\Cli.csproj", "..\\Core\\Core.csproj")},
		"tools/Cli/Cli.csproj": {Data: []byte("<Project/>")},
		"legacy/Old.csproj":    {Data: []byte("<Project/>")},
		"legacy/global.json":   {Data: []byte("{}")},
	})
	var ids []string
	for _, p := range projects {
		ids = append(ids, p.ID+"="+p.Attributes["projectFiles"]+"|"+p.Attributes["solutions"])
	}
	// Core is in App.sln and tools/Tools.sln; only App.sln is at or above it.
	want := "nuget:.=Core/Core.csproj,Web/Web.csproj|App.sln;nuget:legacy=legacy/Old.csproj|;nuget:tools=tools/Cli/Cli.csproj|"
	if got := strings.Join(ids, ";"); got != want {
		t.Errorf("projects:\n got  %s\n want %s", got, want)
	}
	if got := strings.Join(projects[0].Manifests, ","); got != "Core/Core.csproj,Web/Web.csproj,App.sln" {
		t.Errorf("solution manifests: %s", got)
	}
	if got := strings.Join(projects[1].Manifests, ","); got != "legacy/Old.csproj,legacy/global.json" {
		t.Errorf("legacy manifests: %s", got)
	}
}

func TestNearestSolutionTies(t *testing.T) {
	if got, ok := nearestSolution("a/b", []string{"a/Z.sln", "a/A.sln", "B.sln"}); !ok || got != "a/A.sln" {
		t.Errorf("deepest, then first by path: %q", got)
	}
	if _, ok := nearestSolution("a", []string{"b/X.sln"}); ok {
		t.Error("a solution beside the project file does not bound it")
	}
	if got, ok := nearestSolution("a", []string{"Root.sln"}); !ok || got != "Root.sln" {
		t.Errorf("root solution: %q", got)
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
	// MSBuild names a path that does not exist: the project file is restored
	// on its own once more, and still nothing is found.
	missing := &sdktest.FakeTool{ToolName: "dotnet", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] == "msbuild" {
			return sdk.RunResult{Stdout: []byte("/nonexistent/project.assets.json")}, nil
		}
		return sdk.RunResult{}, nil
	}}
	p, req = setup(t, missing)
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "project.assets.json not found") {
		t.Errorf("missing custom assets: %v", err)
	}
	restores := 0
	for _, inv := range missing.Invocations {
		if inv.Args[0] == "restore" {
			restores++
		}
	}
	if restores != 2 {
		t.Errorf("restore, then a fallback restore: %d", restores)
	}
	// MSBuild names a directory: it exists but cannot be read as assets.
	dir := t.TempDir()
	unreadable := &sdktest.FakeTool{ToolName: "dotnet", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] == "msbuild" {
			return sdk.RunResult{Stdout: []byte(dir)}, nil
		}
		return sdk.RunResult{}, nil
	}}
	p, req = setup(t, unreadable)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("unreadable assets")
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

// restoreAll is a fake dotnet that writes assets for every given member on
// any restore and records the restore targets.
func restoreAll(root string, assets []byte, members []string, targets *[]string) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "dotnet", ToolVersion: "8.0.404", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] == "restore" {
			rel, _ := filepath.Rel(root, spec.Args[1])
			*targets = append(*targets, filepath.ToSlash(rel))
			for _, m := range members {
				obj := filepath.Join(root, filepath.Dir(filepath.FromSlash(m)), "obj")
				_ = os.MkdirAll(obj, 0o755)
				_ = os.WriteFile(filepath.Join(obj, "project.assets.json"), assets, 0o600)
			}
		}
		return sdk.RunResult{}, nil
	}}
}

func TestProduceRestoresOncePerSolution(t *testing.T) {
	run := func(files map[string]string, attrs map[string]string) []string {
		root := t.TempDir()
		var members []string
		for rel, content := range files {
			_ = os.MkdirAll(filepath.Join(root, filepath.Dir(filepath.FromSlash(rel))), 0o755)
			_ = os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644)
			if strings.HasSuffix(rel, "proj") {
				members = append(members, rel)
			}
		}
		sort.Strings(members)
		var targets []string
		p := New()
		_ = p.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{
			"dotnet": restoreAll(root, fixture(t), members, &targets)}}})
		attrs["projectFiles"] = strings.Join(members, ",")
		project := sdk.Project{ID: "nuget:.", Ecosystem: ID, Dir: ".", Manifests: members, Attributes: attrs}
		if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project,
			Workspace: sdk.NewWorkspace(root, nil)}); err != nil {
			t.Fatal(err)
		}
		return targets
	}
	files := map[string]string{}
	var listed []string
	for i := 0; i < 30; i++ {
		rel := fmt.Sprintf("src/P%02d/P%02d.csproj", i, i)
		files[rel] = "<Project/>"
		listed = append(listed, strings.ReplaceAll(rel, "/", "\\"))
	}
	files["App.sln"] = string(sln(listed...))
	if got := run(files, map[string]string{"solutions": "App.sln"}); strings.Join(got, ",") != "App.sln" {
		t.Errorf("thirty projects, one restore: %v", got)
	}
	cpm := map[string]string{
		"Web/Web.csproj":       `<Project><ItemGroup><ProjectReference Include="..\Core\Core.csproj" /></ItemGroup></Project>`,
		"Core/Core.csproj":     "<Project/>",
		"Worker/Worker.csproj": "<Project/>",
	}
	if got := run(cpm, map[string]string{}); strings.Join(got, ",") != "Web/Web.csproj,Worker/Worker.csproj" {
		t.Errorf("only unreferenced members are restored: %v", got)
	}
	cycle := map[string]string{
		"A/A.csproj": `<Project><ItemGroup><ProjectReference Include="..\B\B.csproj" /></ItemGroup></Project>`,
		"B/B.csproj": `<Project><ItemGroup><ProjectReference Include="..\A\A.csproj" /></ItemGroup></Project>`,
	}
	if got := run(cycle, map[string]string{}); strings.Join(got, ",") != "A/A.csproj,B/B.csproj" {
		t.Errorf("a reference cycle restores every member: %v", got)
	}
}

func TestProduceRestoresSkippedProjectFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"Web/Web.csproj", "Tools/Tools.csproj"} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(filepath.FromSlash(rel))), 0o755)
		_ = os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("<Project/>"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(root, "App.sln"), sln("Web\\Web.csproj", "Tools\\Tools.csproj"), 0o644)
	assets := fixture(t)
	var targets []string
	failTools := false
	// The solution restore builds only Web (Tools has no configuration
	// mapping); a project file restore writes its own assets.
	tool := &sdktest.FakeTool{ToolName: "dotnet", ToolVersion: "8.0.404", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] != "restore" {
			return sdk.RunResult{}, nil
		}
		rel, _ := filepath.Rel(root, spec.Args[1])
		rel = filepath.ToSlash(rel)
		targets = append(targets, rel)
		dir := filepath.Dir(rel)
		if rel == "App.sln" {
			dir = "Web"
		} else if failTools {
			return sdk.RunResult{}, &sdk.ExitError{Tool: "dotnet", ExitCode: 1}
		}
		obj := filepath.Join(root, filepath.FromSlash(dir), "obj")
		_ = os.MkdirAll(obj, 0o755)
		return sdk.RunResult{}, os.WriteFile(filepath.Join(obj, "project.assets.json"), assets, 0o600)
	}}
	p := New()
	_ = p.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"dotnet": tool}}})
	members := []string{"Tools/Tools.csproj", "Web/Web.csproj"}
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Workspace: sdk.NewWorkspace(root, nil), Project: sdk.Project{
		ID: "nuget:.", Ecosystem: ID, Dir: ".", Manifests: members,
		Attributes: map[string]string{"projectFiles": strings.Join(members, ","), "solutions": "App.sln"}}}
	g, err := p.Produce(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(targets, ",") != "App.sln,Tools/Tools.csproj" {
		t.Errorf("solution, then the skipped project file: %v", targets)
	}
	if mods := g.(*deps.Graph).Modules(); len(mods) != 2 {
		t.Errorf("both members are modules: %v", mods)
	}
	_ = os.RemoveAll(filepath.Join(root, "Tools", "obj"))
	_ = os.RemoveAll(filepath.Join(root, "Web", "obj"))
	failTools = true
	var xe *sdk.ExitError
	if _, err := p.Produce(context.Background(), req); !errors.As(err, &xe) {
		t.Errorf("failed fallback restore: %v", err)
	}
}
