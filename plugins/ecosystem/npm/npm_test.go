package npm

import (
	"context"
	"errors"
	"io/fs"
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

func newPlugin(t *testing.T, tools ...*sdktest.FakeTool) *Plugin {
	t.Helper()
	ft := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	for _, tool := range tools {
		ft.Tools[tool.ToolName] = tool
	}
	p := New()
	if err := p.Init(context.Background(), &sdktest.FakeHost{ToolSet: ft}); err != nil {
		t.Fatal(err)
	}
	return p
}

// variant copies a recorded fixture into a fresh workspace and detects it.
func variant(t *testing.T, p *Plugin, name string) (*sdk.Workspace, sdk.Project) {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", name), root)
	ws := sdk.NewWorkspace(root, nil)
	projects, err := p.Detect(context.Background(), ws)
	if err != nil || len(projects) != 1 {
		t.Fatalf("detect %s: %+v %v", name, projects, err)
	}
	return ws, projects[0]
}

func TestDetect(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"package.json":                         {Data: []byte(`{"workspaces":["packages/*"]}`)},
		"package-lock.json":                    {Data: []byte(`{}`)},
		".npmrc":                               {Data: []byte(`x`)},
		"packages/lib/package.json":            {Data: []byte(`{"name":"lib"}`)},
		"packages/lib/fixtures/package.json":   {Data: []byte(`{"dependencies":{"x":"1"}}`)},
		"standalone/package.json":              {Data: []byte(`{"dependencies":{"x":"1"}}`)},
		"tools/cli/package.json":               {Data: []byte(`{"name":"cli"}`)},
		"tools/cli/yarn.lock":                  {Data: []byte(``)},
		"pnpm-app/package.json":                {Data: []byte(`{"packageManager":"yarn@1.22.0"}`)},
		"pnpm-app/pnpm-workspace.yaml":         {Data: []byte("packages:\n  - apps/*\n")},
		"pnpm-app/apps/web/package.json":       {Data: []byte(`{"dependencies":{"react":"18"}}`)},
		"pnpm-app/node_modules/x/package.json": {Data: []byte(`{}`)},
	}}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, _ := p.Detect(context.Background(), ws)
	var got []string
	for _, pr := range projects {
		got = append(got, pr.ID+" "+pr.Attributes["packageManager"]+" "+strings.Join(pr.Manifests, ","))
	}
	want := []string{
		"npm:. npm package.json,packages/lib/package.json,package-lock.json,.npmrc",
		"npm:pnpm-app pnpm pnpm-app/package.json,pnpm-app/apps/web/package.json,pnpm-app/pnpm-workspace.yaml",
		"npm:tools/cli yarn tools/cli/package.json,tools/cli/yarn.lock",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("projects:\n%s", strings.Join(got, "\n"))
	}
	standalone := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"a/package.json":     {Data: []byte(`{"packageManager":"pnpm@9.15.0","dependencies":{"x":"1"}}`)},
		"b/package.json":     {Data: []byte(`{"name":"no-deps"}`)},
		"c/package.json":     {Data: []byte(`{broken`)},
		"d/package.json":     {Data: []byte(`{"devDependencies":{"y":"1"},"packageManager":"bun@1"}`)},
		"e/package.json":     {Data: []byte(`{"workspaces":["p/*"]}`)},
		"e/p/q/package.json": {Data: []byte(`{"dependencies":{"z":"1"}}`)},
	}}
	projects, _ = p.Detect(context.Background(), standalone)
	got = nil
	for _, pr := range projects {
		got = append(got, pr.ID+" "+pr.Attributes["packageManager"]+" "+pr.Attributes["packages"])
	}
	if strings.Join(got, ";") != "npm:a pnpm a;npm:d npm d;npm:e npm e,e/p/q" {
		t.Errorf("standalone: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
	unreadable := &sdk.Workspace{Root: "/repo", FS: brokenFS{fstest.MapFS{"package.json": {Data: []byte(`{}`)}}}}
	if _, err := p.Detect(context.Background(), unreadable); err == nil {
		t.Error("unreadable package.json")
	}
}

// brokenFS lists files it cannot read.
type brokenFS struct{ fstest.MapFS }

func (b brokenFS) Open(name string) (fs.File, error) {
	if name == "." {
		return b.MapFS.Open(name)
	}
	return nil, fs.ErrPermission
}

func (b brokenFS) ReadFile(string) ([]byte, error) { return nil, fs.ErrPermission }

func TestProduceRecorded(t *testing.T) {
	for name, want := range map[string]int{"npm": 6, "npm6": 4, "yarn1": 6, "berry": 6, "pnpm9": 6, "pnpm8": 6, "pnpm7": 6} {
		t.Run(name, func(t *testing.T) {
			p := newPlugin(t)
			ws, project := variant(t, p, name)
			g := sdktest.TestFactProducer(t, p, sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws}).(*deps.Graph)
			if len(g.Packages()) != want || g.Root != "project:npm:." {
				t.Errorf("graph: %d packages, root %s", len(g.Packages()), g.Root)
			}
		})
	}
}

func TestProduceErrors(t *testing.T) {
	p := newPlugin(t)
	ws, project := variant(t, p, "npm")
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "x", Project: project, Workspace: ws}); err == nil {
		t.Error("unknown fact")
	}
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws}
	_ = os.WriteFile(filepath.Join(ws.Root, "package-lock.json"), []byte("{"), 0o644)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("broken lockfile")
	}
	_ = os.Remove(filepath.Join(ws.Root, "package-lock.json"))
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("missing lockfile")
	}
	_ = os.WriteFile(filepath.Join(ws.Root, "package.json"), []byte("{"), 0o644)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("broken package.json")
	}
	_ = os.Remove(filepath.Join(ws.Root, "package.json"))
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("missing package.json")
	}
	req.Project.Attributes = map[string]string{"packages": ""}
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("no packages")
	}
	req.Project.Attributes = map[string]string{"packages": ".", "lockfile": "x.lock"}
	_ = os.WriteFile(filepath.Join(ws.Root, "package.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(ws.Root, "x.lock"), []byte("{}"), 0o644)
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "unknown lockfile") {
		t.Errorf("unknown lockfile: %v", err)
	}
}

// lockWriter returns a fake package manager that writes a lockfile (and a
// node_modules directory) into the directory it runs in.
func lockWriter(name, version, lockName string, lock []byte) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: name, ToolVersion: version, RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		_ = os.MkdirAll(filepath.Join(spec.Dir, "node_modules", "x"), 0o755)
		return sdk.RunResult{}, os.WriteFile(filepath.Join(spec.Dir, lockName), lock, 0o644)
	}}
}

func TestProduceGeneratesLockfile(t *testing.T) {
	npm6, _ := os.ReadFile("testdata/npm6/package-lock.json")
	yarnLock, _ := os.ReadFile("testdata/yarn1/yarn.lock")
	pnpmLock, _ := os.ReadFile("testdata/pnpm9/pnpm-lock.yaml")
	cases := []struct {
		manager, version, lock string
		data                   []byte
		args                   string
	}{
		{"npm", "10.9.0", "package-lock.json", npm6, "install --package-lock-only --ignore-scripts --no-audit --no-fund"},
		{"yarn", "1.22.22", "yarn.lock", yarnLock, "install --ignore-scripts --non-interactive --no-progress --modules-folder"},
		{"yarn", "4.5.3", "yarn.lock", yarnLock, "install --mode=update-lockfile --no-immutable"},
		{"yarn", "garbage", "yarn.lock", yarnLock, "--modules-folder"},
		{"pnpm", "9.15.0", "pnpm-lock.yaml", pnpmLock, "install --lockfile-only --ignore-scripts --no-frozen-lockfile"},
	}
	for _, c := range cases {
		t.Run(c.manager+c.version, func(t *testing.T) {
			tool := lockWriter(c.manager, c.version, c.lock, c.data)
			p := newPlugin(t, tool)
			root := t.TempDir()
			copyTree(t, "testdata/npm6", root)
			_ = os.Remove(filepath.Join(root, "package-lock.json"))
			pm := `"packageManager": "` + c.manager + `@` + c.version + `", `
			pj, _ := os.ReadFile(filepath.Join(root, "package.json"))
			_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(strings.Replace(string(pj), "{", "{"+pm, 1)), 0o644)
			ws := sdk.NewWorkspace(root, nil)
			projects, _ := p.Detect(context.Background(), ws)
			if len(projects) != 1 || projects[0].Attributes["lockfile"] != "" {
				t.Fatalf("projects: %+v", projects)
			}
			g, err := p.Produce(context.Background(), sdk.FactRequest{Name: deps.FactDependencyGraph, Project: projects[0], Workspace: ws})
			if err != nil {
				t.Fatal(err)
			}
			if len(g.(*deps.Graph).Packages()) < 4 {
				t.Errorf("graph: %+v", g)
			}
			if args := strings.Join(tool.Invocations[0].Args, " "); !strings.Contains(args, c.args) || tool.Invocations[0].Env["COREPACK_ENABLE_DOWNLOAD_PROMPT"] != "0" {
				t.Errorf("args: %s", args)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 1 {
				t.Errorf("generated files left behind: %v", entries)
			}
		})
	}
	// Failures: no tool, tool error, no lockfile written, not initialized.
	root := t.TempDir()
	copyTree(t, "testdata/npm6", root)
	_ = os.Remove(filepath.Join(root, "package-lock.json"))
	ws := sdk.NewWorkspace(root, nil)
	projects, _ := New().Detect(context.Background(), ws)
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Project: projects[0], Workspace: ws}
	if _, err := newPlugin(t).Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no tool: %v", err)
	}
	failing := &sdktest.FakeTool{ToolName: "npm", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "npm", ExitCode: 1}
	}}
	if _, err := newPlugin(t, failing).Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "failed to update the lockfile") {
		t.Errorf("tool failure: %v", err)
	}
	if _, err := newPlugin(t, &sdktest.FakeTool{ToolName: "npm"}).Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Errorf("no lockfile: %v", err)
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	req.Project.Dir = "missing"
	req.Project.Attributes["packages"] = "."
	if _, err := newPlugin(t, &sdktest.FakeTool{ToolName: "npm"}).Produce(context.Background(), req); err == nil {
		t.Error("missing project dir")
	}
}

func TestVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "vnd.npm.install-v1+json") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.EscapedPath() {
		case "/lodash":
			_, _ = w.Write([]byte(`{"versions":{"4.17.20":{},"4.17.21":{"deprecated":false},"4.17.22":{"deprecated":"do not use"},"4.17.23":{"deprecated":""}}}`))
		case "/private/@acme%2Fui":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"versions":{"1.0.0":{}}}`))
		case "/basic/@corp%2Fx":
			if u, pw, ok := r.BasicAuth(); !ok || u != "u" || pw != "p" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"versions":{"2.0.0":{}}}`))
		case "/broken":
			_, _ = w.Write([]byte(`{`))
		case "/empty":
			_, _ = w.Write([]byte(`{"versions":{}}`))
		case "/down":
			w.WriteHeader(http.StatusBadGateway)
		case "/teapot":
			w.WriteHeader(http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := New()
	p.cfg = Config{Registries: []Registry{{URL: srv.URL + "/private/", Scope: "@acme", TokenEnv: "TOK"}, {URL: srv.URL + "/basic", Scope: "@corp", UsernameEnv: "U", PasswordEnv: "P"}, {URL: srv.URL}}}
	p.Getenv = func(k string) string { return map[string]string{"TOK": "tok", "U": "u", "P": "p"}[k] }
	if err := p.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{NoCache: true}}); err != nil {
		t.Fatal(err)
	}
	p.registry.http.Retries, p.registry.http.Backoff = 1, time.Millisecond
	project := sdk.Project{ID: "npm:."}
	check := func(pkg, want string) {
		t.Helper()
		vs, err := p.Versions(context.Background(), project, pkg)
		if err != nil || strings.Join(vs, ",") != want {
			t.Errorf("%s: %v %v", pkg, vs, err)
		}
	}
	check("pkg:npm/lodash", "4.17.20,4.17.21,4.17.23")
	check("pkg:npm/%40acme/ui", "1.0.0")
	check("pkg:npm/%40corp/x@1", "2.0.0")
	for pkg, wantNotFound := range map[string]bool{"pkg:npm/missing": true, "pkg:npm/empty": true, "pkg:npm/broken": false, "pkg:npm/down": false, "pkg:npm/teapot": false} {
		_, err := p.Versions(context.Background(), project, pkg)
		if err == nil || errors.Is(err, deps.ErrPackageNotFound) != wantNotFound {
			t.Errorf("%s: %v", pkg, err)
		}
	}
	if _, err := p.Versions(context.Background(), project, "nope"); err == nil {
		t.Error("bad purl")
	}
	if _, err := New().Versions(context.Background(), project, "pkg:npm/x"); err == nil {
		t.Error("not initialized")
	}
	// Without an unscoped registry the public one is added.
	q := New()
	q.cfg.Registries = []Registry{{URL: "https://npm.acme.example", Scope: "@acme"}}
	_ = q.Init(context.Background(), &sdktest.FakeHost{})
	if r := q.registry.forPackage("left-pad"); r.URL != DefaultRegistry {
		t.Errorf("default registry: %+v", r)
	}
	if r := (&registryClient{}).forPackage("x"); r.URL != DefaultRegistry {
		t.Error("empty client")
	}
	if New().getenv("") != "" || New().getenv("PATH") == "" {
		t.Error("getenv")
	}
}

func TestConfig(t *testing.T) {
	sdktest.TestPlugin(t, New())
	for _, bad := range []Config{{Registries: []Registry{{}}}, {Registries: []Registry{{URL: "x", Scope: "acme"}}}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v must be invalid", bad)
		}
	}
	if err := (&Config{Registries: []Registry{{URL: "x", Scope: "@acme"}}}).Validate(); err != nil {
		t.Error(err)
	}
	if New().ToolRequirements() != nil || !New().AppliesTo(sdk.Project{Ecosystem: ID}) || New().Produces()[0] != deps.FactDependencyGraph {
		t.Error("contracts")
	}
}
