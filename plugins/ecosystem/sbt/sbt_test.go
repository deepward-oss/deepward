package sbt

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

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/graph.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDetect(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"backend/build.sbt":                  {Data: []byte("")},
		"backend/core/build.sbt":             {Data: []byte("")},
		"backend/project/build.properties":   {Data: []byte("sbt.version=1.10.7")},
		"backend/project/plugins.sbt":        {Data: []byte("")},
		"backend/project/Dependencies.scala": {Data: []byte("")},
		"backend/project/project/x.sbt":      {Data: []byte("")},
		"tools/project/build.properties":     {Data: []byte("")},
		"examples/demo/build.sbt":            {Data: []byte("")},
		"services/api/pom.xml":               {Data: []byte("")},
	}, Exclude: []string{"examples"}}
	p := New()
	sdktest.TestEcosystem(t, p, ws)
	projects, _ := p.Detect(context.Background(), ws)
	if len(projects) != 2 || projects[0].ID != "sbt:backend" || projects[1].ID != "sbt:tools" {
		t.Fatalf("projects: %+v", projects)
	}
	m := strings.Join(projects[0].Manifests, ",")
	if m != "backend/build.sbt,backend/core/build.sbt,backend/project/Dependencies.scala,backend/project/build.properties,backend/project/plugins.sbt,backend/project/project/x.sbt" {
		t.Errorf("manifests: %s", m)
	}
	root := &sdk.Workspace{Root: "/r", FS: fstest.MapFS{"build.sbt": {}, "sub/build.sbt": {}}}
	projects, _ = p.Detect(context.Background(), root)
	if len(projects) != 1 || projects[0].ID != "sbt:." || len(projects[0].Manifests) != 2 {
		t.Errorf("root build: %+v", projects)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
}

func sbtTool(t *testing.T, content []byte, exitErr error) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "sbt", ToolVersion: "1.10.7", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		for _, a := range spec.Args {
			if strings.HasPrefix(a, "--addPluginSbtFile=") && strings.HasSuffix(a, "deepward.sbt") {
				b, err := os.ReadFile(strings.TrimPrefix(a, "--addPluginSbtFile="))
				if err != nil || !strings.Contains(string(b), "Compile / unmanagedSourceDirectories += file(") {
					t.Errorf("plugin file: %s %v", b, err)
				}
			}
			if out, ok := strings.CutPrefix(a, "-Ddeepward.out="); ok && content != nil {
				src, _ := os.ReadFile(filepath.Join(filepath.Dir(out), "src", "DeepwardGraphPlugin.scala"))
				if !strings.Contains(string(src), "object DeepwardGraphPlugin") {
					t.Error("embedded plugin source not written")
				}
				_ = os.WriteFile(out, content, 0o600)
			}
		}
		return sdk.RunResult{Tail: []string{"[error] resolution failed"}}, exitErr
	}}
}

func setup(t *testing.T, tool *sdktest.FakeTool, addons ...sdk.Addon) (*Plugin, sdk.FactRequest) {
	t.Helper()
	p := New()
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	if tool != nil {
		tools.Tools["sbt"] = tool
	}
	_ = p.Init(context.Background(), &sdktest.FakeHost{ToolSet: tools, AddonList: addons, Run: sdk.RunSettings{CacheDir: t.TempDir()}})
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "build.sbt"), []byte(""), 0o644)
	project := sdk.Project{ID: "sbt:.", Ecosystem: ID, Dir: ".", Manifests: []string{"build.sbt"}}
	return p, sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: sdk.NewWorkspace(root, nil)}
}

func TestProduce(t *testing.T) {
	tool := sbtTool(t, fixture(t), nil)
	extra := sdk.Addon{Name: "corp", Kind: sdk.AddonSbtPluginFile, File: "ci/corp.sbt"}
	builtin := sdk.Addon{Name: "graph-dump", Kind: sdk.AddonSbtPluginFile, Builtin: true, File: "x"}
	p, req := setup(t, tool, builtin, extra)
	g := sdktest.TestFactProducer(t, p, req).(*deps.Graph)
	if len(g.Packages()) < 10 || g.Root != "project:sbt:." {
		t.Errorf("graph: %d", len(g.Packages()))
	}
	args := strings.Join(tool.Invocations[0].Args, " ")
	if !strings.Contains(args, "--addPluginSbtFile="+filepath.Join(req.Workspace.Root, "ci", "corp.sbt")) || !strings.HasSuffix(args, "+deepwardGraph") || !strings.Contains(args, "-batch") {
		t.Errorf("args: %s", args)
	}
	if _, err := os.Stat(filepath.Join(req.Workspace.Root, "target", "deepward")); !os.IsNotExist(err) {
		t.Error("temporary files must be removed")
	}
	off := false
	p.cfg.CrossBuild = &off
	_, _ = p.Produce(context.Background(), req)
	if last := tool.Invocations[len(tool.Invocations)-1].Args; last[len(last)-1] != "deepwardGraph" {
		t.Errorf("crossBuild false: %v", last)
	}
}

func TestProduceFailures(t *testing.T) {
	p, req := setup(t, sbtTool(t, nil, &sdk.ExitError{Tool: "sbt", ExitCode: 1, Tail: []string{"[error] resolution failed"}}))
	_, err := p.Produce(context.Background(), req)
	var xe *sdk.ExitError
	if !errors.As(err, &xe) {
		t.Errorf("exit error: %v", err)
	}
	p, req = setup(t, nil)
	if _, err := p.Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("tool missing: %v", err)
	}
	p, req = setup(t, sbtTool(t, nil, nil))
	if _, err := p.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "wrote no dependency graph") {
		t.Errorf("no output: %v", err)
	}
	p, req = setup(t, sbtTool(t, []byte("{oops\n"), nil))
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("bad output")
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "x"}); err == nil {
		t.Error("unknown fact")
	}
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	p, req = setup(t, sbtTool(t, fixture(t), nil))
	req.Workspace = sdk.NewWorkspace(file, nil)
	if _, err := p.Produce(context.Background(), req); err == nil {
		t.Error("unwritable target directory")
	}
}

func TestVersionsKeepScalaSuffix(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`<metadata><versioning><versions><version>2.9.0</version><version>2.13.0</version></versions></versioning></metadata>`))
	}))
	defer srv.Close()
	p := New()
	p.cfg = Config{Repositories: []Repository{{ID: "c", URL: srv.URL, TokenEnv: "T"}}}
	p.Getenv = func(string) string { return "" }
	_ = p.Init(context.Background(), &sdktest.FakeHost{})
	vs, err := p.Versions(context.Background(), sdk.Project{}, "pkg:maven/org.typelevel/cats-core_2.13")
	if err != nil || len(vs) != 2 || paths[0] != "/org/typelevel/cats-core_2.13/maven-metadata.xml" {
		t.Errorf("versions: %v %v %v", vs, err, paths)
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "bad"); err == nil {
		t.Error("bad purl")
	}
	if _, err := New().Versions(context.Background(), sdk.Project{}, "pkg:maven/a/b"); err == nil {
		t.Error("not initialized")
	}
	if (&Config{Repositories: []Repository{{}}}).Validate() == nil || (&Config{}).Validate() != nil {
		t.Error("validate")
	}
	if New().ConfigTarget() == nil || New().Addons()[0].Name != "graph-dump" || New().ToolRequirements()[0].Constraint != MinSbt ||
		!New().AppliesTo(sdk.Project{Ecosystem: "sbt"}) || New().Produces()[0] != deps.FactDependencyGraph || New().getenv("PATH") == "" && os.Getenv("PATH") != "" {
		t.Error("metadata")
	}
}

func TestRealize(t *testing.T) {
	root, err := filepath.Abs("../../../testdata/projects/sbt-app")
	if err != nil {
		t.Fatal(err)
	}
	ws := sdk.NewWorkspace(root, nil)
	project := sdk.Project{ID: "sbt:.", Ecosystem: ID, Dir: ".", Manifests: []string{"build.sbt", "project/build.properties"}}
	res, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{
		{PURL: "pkg:maven/io.circe/circe-core_2.13", To: "0.14.10"},
		{PURL: "pkg:maven/org.apache.logging.log4j/log4j-core", To: "2.17.1"},
		{PURL: "pkg:maven/org.typelevel/cats-kernel_2.13", To: "2.13.0", Pin: true},
		{PURL: "garbage", To: "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edits) != 2 || len(res.Manual) != 2 || !strings.Contains(res.Manual[0].Suggestion, "dependencyOverrides") {
		t.Errorf("result: %+v", res)
	}
	if _, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: sdk.Project{Manifests: []string{"missing.sbt"}}, Workspace: ws}); err == nil {
		t.Error("missing file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Realize(ctx, sdk.RealizeRequest{Project: project, Workspace: ws}); err == nil {
		t.Error("cancelled")
	}
}
