package maven

import (
	"context"
	"errors"
	iofs "io/fs"
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

func sampleWorkspace(t *testing.T) *sdk.Workspace {
	t.Helper()
	src, err := filepath.Abs("../../../testdata/projects/maven-multi")
	if err != nil {
		t.Fatal(err)
	}
	// Work on a copy: producing the graph writes under target/.
	root := t.TempDir()
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			if info.Name() == "target" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return sdk.NewWorkspace(root, nil)
}

func TestDetectReactor(t *testing.T) {
	p := New()
	sdktest.TestEcosystem(t, p, sampleWorkspace(t))
	projects, err := p.Detect(context.Background(), sampleWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != "maven:." || strings.Join(projects[0].Manifests, ",") != "pom.xml,core/pom.xml,web/pom.xml" {
		t.Fatalf("projects: %+v", projects)
	}
	if projects[0].Attributes["modules"] != "2" {
		t.Error("modules attribute")
	}
}

func TestDetectMonorepoAndExcludes(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"services/api/pom.xml":        {Data: []byte("<project><modules><module>impl</module><module>missing</module></modules></project>")},
		"services/api/impl/pom.xml":   {Data: []byte("<project/>")},
		"services/api/target/pom.xml": {Data: []byte("<project/>")},
		"examples/demo/pom.xml":       {Data: []byte("<project/>")},
		"broken/pom.xml":              {Data: []byte("<project>")},
		"self/pom.xml":                {Data: []byte("<project><modules><module>.</module></modules></project>")},
		"backend/build.sbt":           {Data: []byte("")},
	}, Exclude: []string{"examples/"}}
	projects, err := New().Detect(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "maven:broken,maven:self,maven:services/api" {
		t.Errorf("projects: %v", ids)
	}
	if strings.Join(projects[2].Manifests, ",") != "services/api/pom.xml,services/api/impl/pom.xml" {
		t.Errorf("manifests: %v", projects[2].Manifests)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Detect(ctx, ws); err == nil {
		t.Error("cancelled")
	}
	bad := &sdk.Workspace{FS: errFS{}}
	if _, err := New().Detect(context.Background(), bad); err == nil {
		t.Error("walk error")
	}
}

type errFS struct{}

func (errFS) Open(string) (iofs.File, error) { return nil, errors.New("boom") }

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/tree-multi.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func treeTool(t *testing.T, content []byte, exitErr error) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "mvn", ToolVersion: "3.9.9", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		for _, a := range spec.Args {
			if out, ok := strings.CutPrefix(a, "-DoutputFile="); ok && content != nil {
				if err := os.WriteFile(out, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return sdk.RunResult{Tail: []string{"[ERROR] boom"}}, exitErr
	}}
}

func initPlugin(t *testing.T, p *Plugin, tool *sdktest.FakeTool, addons ...sdk.Addon) {
	t.Helper()
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	if tool != nil {
		tools.Tools["mvn"] = tool
	}
	if err := p.Init(context.Background(), &sdktest.FakeHost{ToolSet: tools, AddonList: addons, Run: sdk.RunSettings{CacheDir: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
}

func TestProduceGraph(t *testing.T) {
	p := New()
	p.Getenv = func(k string) string { return map[string]string{"DEEPWARD_MAVEN_SETTINGS": "ci/settings.xml"}[k] }
	tool := treeTool(t, fixture(t), nil)
	initPlugin(t, p, tool, sdk.Addon{Name: "dependency-plugin", Kind: sdk.AddonMavenGoal, Coordinates: "org.apache.maven.plugins:maven-dependency-plugin", Version: "3.6.1", Goal: "tree"})
	ws := sampleWorkspace(t)
	project := sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml"}}
	v := sdktest.TestFactProducer(t, p, sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws})
	g := v.(*deps.Graph)
	if g.Root != "project:maven:." || g.Source != "mvn org.apache.maven.plugins:maven-dependency-plugin:3.6.1:tree" || len(g.Packages()) != 9 {
		t.Errorf("graph: root=%s source=%s n=%d", g.Root, g.Source, len(g.Packages()))
	}
	args := strings.Join(tool.Invocations[0].Args, " ")
	for _, want := range []string{"-B -q", "-s " + filepath.Join(ws.Root, "ci", "settings.xml"), "maven-dependency-plugin:3.6.1:tree", "-DoutputType=json", "-DappendOutput=true", "-f " + filepath.Join(ws.Root, "pom.xml")} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	p.cfg.Settings = "/abs/settings.xml"
	_, _ = p.Produce(context.Background(), sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws})
	if !strings.Contains(strings.Join(tool.Invocations[len(tool.Invocations)-1].Args, " "), "-s /abs/settings.xml") {
		t.Error("absolute settings path")
	}
}

func TestProduceFailures(t *testing.T) {
	ws := sampleWorkspace(t)
	project := sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: "."}
	req := sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: ws}

	p := New()
	initPlugin(t, p, treeTool(t, nil, &sdk.ExitError{Tool: "mvn", ExitCode: 1, Tail: []string{"[ERROR] boom"}}))
	_, err := p.Produce(context.Background(), req)
	var xe *sdk.ExitError
	if !errors.As(err, &xe) || xe.Tail[0] != "[ERROR] boom" {
		t.Errorf("build failure must keep the tail: %v", err)
	}
	missing := New()
	initPlugin(t, missing, nil)
	if _, err := missing.Produce(context.Background(), req); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("tool not found: %v", err)
	}
	noOutput := New()
	initPlugin(t, noOutput, treeTool(t, nil, nil))
	if _, err := noOutput.Produce(context.Background(), req); err == nil || !strings.Contains(err.Error(), "wrote no dependency tree") {
		t.Errorf("no output: %v", err)
	}
	garbage := New()
	initPlugin(t, garbage, treeTool(t, []byte("not json"), nil))
	if _, err := garbage.Produce(context.Background(), req); err == nil {
		t.Error("garbage output")
	}
	if _, err := New().Produce(context.Background(), req); err == nil {
		t.Error("not initialized")
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Name: "other"}); err == nil {
		t.Error("unknown fact")
	}
}

func TestVersionsAndMetadata(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path == "/nexus/com/acme/billing-client/maven-metadata.xml" {
			_, _ = w.Write([]byte(`<metadata><versioning><versions><version>1.0</version><version>1.1</version></versions></versioning></metadata>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := New()
	p.cfg = Config{Repositories: []Repository{{ID: "central", URL: srv.URL + "/central"}, {ID: "nexus", URL: srv.URL + "/nexus", UsernameEnv: "NEXUS_USER", PasswordEnv: "NEXUS_PASSWORD"}}}
	p.Getenv = func(k string) string { return map[string]string{"NEXUS_USER": "ci", "NEXUS_PASSWORD": "pw"}[k] }
	initPlugin(t, p, nil)
	vs, err := p.Versions(context.Background(), sdk.Project{}, "pkg:maven/com.acme/billing-client")
	if err != nil || strings.Join(vs, ",") != "1.0,1.1" || !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("versions: %v %v auth=%q", vs, err, auth)
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "pkg:maven/x/y"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("not found: %v", err)
	}
	if _, err := p.Versions(context.Background(), sdk.Project{}, "nope"); err == nil {
		t.Error("bad purl")
	}
	if _, err := New().Versions(context.Background(), sdk.Project{}, "pkg:maven/x/y"); err == nil {
		t.Error("not initialized")
	}
}

func TestConfigAndMetadata(t *testing.T) {
	p := New()
	if (&Config{Repositories: []Repository{{ID: "x"}}}).Validate() == nil || (&Config{}).Validate() != nil {
		t.Error("validate")
	}
	if p.ConfigTarget() == nil || len(p.Addons()) != 1 || p.ToolRequirements()[0].Constraint != MinMaven || !p.AppliesTo(sdk.Project{Ecosystem: "maven"}) ||
		p.Produces()[0] != deps.FactDependencyGraph || p.getenv("PATH") == "" && os.Getenv("PATH") != "" {
		t.Error("plugin metadata")
	}
	if p.addon("dependency-plugin").Version != "3.8.1" || p.addon("nope").Version != "" {
		t.Error("default addon without host")
	}
}

// TestMetadataCache is task 9.2: a repeated lookup within the TTL issues no
// request; --no-cache disables the cache.
func TestMetadataCache(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`<metadata><versioning><versions><version>1.0</version></versions></versioning></metadata>`))
	}))
	defer srv.Close()
	cacheDir := t.TempDir()
	run := func(noCache bool) {
		p := New()
		p.cfg = Config{Repositories: []Repository{{ID: "r", URL: srv.URL}}}
		_ = p.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{CacheDir: cacheDir, NoCache: noCache}})
		if _, err := p.Versions(context.Background(), sdk.Project{}, "pkg:maven/g/a"); err != nil {
			t.Fatal(err)
		}
	}
	run(false)
	run(false)
	if calls != 1 {
		t.Errorf("cached run issued %d requests, want 1", calls)
	}
	run(true)
	if calls != 2 {
		t.Errorf("--no-cache must bypass the cache: %d requests", calls)
	}
}

func TestRealize(t *testing.T) {
	ws := sampleWorkspace(t)
	project := sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml", "core/pom.xml", "web/pom.xml"}}
	res, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{
		{PURL: "pkg:maven/com.fasterxml.jackson.core/jackson-databind", From: "2.12.1", To: "2.12.7.1"},
		{PURL: "pkg:maven/org.apache.logging.log4j/log4j-core", From: "2.14.1", To: "2.17.1"},
		{PURL: "pkg:maven/org.yaml/snakeyaml", From: "1.33", To: "2.0"},
		{PURL: "pkg:maven/org.apache.logging.log4j/log4j-api", To: "2.17.1", Pin: true},
		{PURL: "pkg:maven/not/declared", To: "1"},
		{PURL: "garbage", To: "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, e := range res.Edits {
		paths[e.Path]++
	}
	// jackson + log4j properties and the pin in the root POM; snakeyaml in web.
	if paths["pom.xml"] != 3 || paths["web/pom.xml"] != 1 || len(res.Manual) != 2 {
		t.Errorf("edits %v, manual %+v", paths, res.Manual)
	}
	if _, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: sdk.Project{ID: "x", Manifests: []string{"missing.xml"}}, Workspace: ws}); err == nil {
		t.Error("missing manifest")
	}
	if _, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: sdk.Project{ID: "x"}, Workspace: ws}); err == nil {
		t.Error("no POM")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Realize(ctx, sdk.RealizeRequest{Project: project, Workspace: ws}); err == nil {
		t.Error("cancelled")
	}
}
