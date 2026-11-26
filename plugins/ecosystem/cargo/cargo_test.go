package cargo

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

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func plugin(t *testing.T, tool *sdktest.FakeTool) *Plugin {
	t.Helper()
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	if tool != nil {
		tools.Tools["cargo"] = tool
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
	copyTree(t, "testdata/cargoapp", root)
	ws := sdk.NewWorkspace(root, nil)
	projects, err := New().Detect(context.Background(), ws)
	if err != nil || len(projects) != 1 {
		t.Fatalf("detect: %+v %v", projects, err)
	}
	return ws, projects[0]
}

func TestDetect(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"Cargo.toml":                {Data: []byte("[workspace]\nmembers = [\"crates/*\"]\nexclude = [\"examples\"]\n")},
		"Cargo.lock":                {Data: []byte("version = 4\n")},
		".cargo/config.toml":        {Data: []byte("")},
		"crates/a/Cargo.toml":       {Data: []byte("[package]\nname = \"a\"\n")},
		"crates/b/Cargo.toml":       {Data: []byte("[package")},
		"examples/demo/Cargo.toml":  {Data: []byte("[package]\nname = \"demo\"\n")},
		"tools/x/Cargo.toml":        {Data: []byte("[dependencies]\n")},
		"target/debug/Cargo.toml":   {Data: []byte("[package]\n")},
		"standalone/Cargo.toml":     {Data: []byte("[package]\nname = \"s\"\n")},
		"standalone/sub/Cargo.toml": {Data: []byte("[package]\nname = \"sub\"\n")},
	}}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, _ := p.Detect(context.Background(), ws)
	var got []string
	for _, pr := range projects {
		got = append(got, pr.ID+" "+strings.Join(pr.Manifests, ","))
	}
	// Crates below a workspace root belong to it unless excluded, as in Cargo.
	want := "cargo:. Cargo.toml,crates/a/Cargo.toml,crates/b/Cargo.toml,standalone/Cargo.toml,standalone/sub/Cargo.toml,Cargo.lock,.cargo/config.toml\n" +
		"cargo:examples/demo examples/demo/Cargo.toml"
	if strings.Join(got, "\n") != want {
		t.Errorf("projects:\n%s", strings.Join(got, "\n"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
	solo := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"a/Cargo.toml":     {Data: []byte("[package]\nname = \"a\"\n")},
		"a/sub/Cargo.toml": {Data: []byte("[package]\nname = \"sub\"\n")},
	}}
	projects, _ = p.Detect(context.Background(), solo)
	if len(projects) != 2 || projects[1].ID != "cargo:a/sub" {
		t.Errorf("standalone crates: %+v", projects)
	}
}

func TestProduce(t *testing.T) {
	p := plugin(t, nil)
	ws, project := workspace(t)
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws}
	g := sdktest.TestFactProducer(t, p, req).(*deps.Graph)
	if len(g.Packages()) != 8 || g.Source != "Cargo.lock" {
		t.Errorf("graph: %d %s", len(g.Packages()), g.Source)
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "x", Project: project, Workspace: ws}); err == nil {
		t.Error("unknown fact")
	}
	lock := filepath.Join(ws.Root, "Cargo.lock")
	data, _ := os.ReadFile(lock)
	_ = os.WriteFile(lock, []byte("[[package"), 0o644)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("broken lock")
	}
	_ = os.Remove(lock)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("missing lock")
	}
	// Without Cargo.lock, cargo generates one, which is removed afterwards.
	gen := &sdktest.FakeTool{ToolName: "cargo", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, os.WriteFile(filepath.Join(spec.Dir, "Cargo.lock"), data, 0o644)
	}}
	project.Attributes["lockfile"] = ""
	req.Project = project
	got, err := plugin(t, gen).Produce(context.Background(), req)
	if err != nil || got.(*deps.Graph).Source != "cargo generate-lockfile" {
		t.Fatalf("generated: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) || strings.Join(gen.Invocations[0].Args, " ") != "generate-lockfile" {
		t.Error("generated Cargo.lock must be removed")
	}
	if _, err := plugin(t, nil).Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no cargo: %v", err)
	}
	failing := &sdktest.FakeTool{ToolName: "cargo", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "cargo", ExitCode: 101}
	}}
	if _, err := plugin(t, failing).Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "generate-lockfile failed") {
		t.Errorf("cargo failure: %v", err)
	}
	if _, err := plugin(t, &sdktest.FakeTool{ToolName: "cargo"}).Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Errorf("no lock written: %v", err)
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	_ = os.WriteFile(filepath.Join(ws.Root, "app", "Cargo.toml"), []byte("[package"), 0o644)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("broken manifest")
	}
	_ = os.Remove(filepath.Join(ws.Root, "app", "Cargo.toml"))
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("missing manifest")
	}
	req.Project.Attributes = map[string]string{}
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("no members")
	}
}

func TestVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/private/in/te/internal":
			if r.Header.Get("Authorization") != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"name":"internal","vers":"0.1.0","yanked":false}` + "\n"))
		case "/crates/re/ge/regex":
			_, _ = w.Write([]byte(`{"name":"regex","vers":"1.5.4","yanked":false}` + "\n" + `{"name":"regex","vers":"1.5.5","yanked":true}` + "\n\n" + `{"name":"regex","vers":"1.5.6"}` + "\n"))
		case "/crates/1/a", "/crates/2/ab", "/crates/3/a/abc":
			_, _ = w.Write([]byte(`{"vers":"1.0.0"}`))
		case "/crates/ba/dj/badjson":
			_, _ = w.Write([]byte(`{`))
		case "/crates/do/wn/down":
			w.WriteHeader(http.StatusBadGateway)
		case "/crates/te/ap/teapot":
			w.WriteHeader(http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := New()
	p.cfg = Config{Registries: []Registry{{Name: "corp", Index: "sparse+" + srv.URL + "/private/", TokenEnv: "TOK"}}}
	p.Getenv = func(k string) string { return map[string]string{"TOK": "secret"}[k] }
	_ = p.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{NoCache: true}})
	p.indexes[len(p.indexes)-1].url = srv.URL + "/crates"
	p.http.Retries, p.http.Backoff = 1, time.Millisecond
	project := sdk.Project{ID: "cargo:."}
	check := func(pkg, want string) {
		t.Helper()
		vs, err := p.Versions(context.Background(), project, pkg)
		if err != nil || strings.Join(vs, ",") != want {
			t.Errorf("%s: %v %v", pkg, vs, err)
		}
	}
	check("pkg:cargo/internal", "0.1.0")
	check("pkg:cargo/regex@1.5.4", "1.5.4,1.5.6")
	check("pkg:cargo/a", "1.0.0")
	check("pkg:cargo/AB", "1.0.0")
	check("pkg:cargo/abc", "1.0.0")
	if _, err := p.Versions(context.Background(), project, "pkg:cargo/missing"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("missing: %v", err)
	}
	for _, bad := range []string{"pkg:cargo/badjson", "pkg:cargo/down", "pkg:cargo/teapot", "nope"} {
		if _, err := p.Versions(context.Background(), project, bad); err == nil || errors.Is(err, deps.ErrPackageNotFound) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := New().Versions(context.Background(), project, "pkg:cargo/x"); err == nil {
		t.Error("not initialized")
	}
	if New().getenv("") != "" || New().getenv("PATH") == "" {
		t.Error("getenv")
	}
}

func TestConfig(t *testing.T) {
	sdktest.TestPlugin(t, New())
	if err := (&Config{Registries: []Registry{{Name: "x"}}}).Validate(); err == nil {
		t.Error("index is required")
	}
	if err := (&Config{Registries: []Registry{{Index: "sparse+https://x"}}}).Validate(); err != nil {
		t.Error(err)
	}
	if New().ToolRequirements() != nil || !New().AppliesTo(sdk.Project{Ecosystem: ID}) || New().Produces()[0] != deps.FactDependencyGraph {
		t.Error("contracts")
	}
}
