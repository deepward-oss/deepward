package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// gen is a fake code generator: it writes app/generated.txt from
// versions.txt, leaves a log file behind and deletes app/stale.txt.
func gen() *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "gen", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		data, err := os.ReadFile(filepath.Join(spec.Dir, "versions.txt"))
		if err != nil {
			return sdk.RunResult{}, err
		}
		_ = os.WriteFile(filepath.Join(spec.Dir, "generated.txt"), []byte("generated from\n"+string(data)), 0o644)
		_ = os.WriteFile(filepath.Join(spec.Dir, "gen.log"), []byte("noise"), 0o644)
		_ = os.Remove(filepath.Join(spec.Dir, "stale.txt"))
		_ = os.Remove(filepath.Join(spec.Dir, "keep.cfg"))
		return sdk.RunResult{}, nil
	}}
}

func engineWithTools(t *testing.T, ws *sdk.Workspace, cfg string, tools ...*sdktest.FakeTool) *Engine {
	t.Helper()
	eco := &manifestEco{sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml"}}
	eco.RealizeFunc = realizer
	reg := sdk.NewRegistry()
	_ = reg.Register(eco)
	_ = reg.Register(fixAnalyzer(t))
	ft := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{}}
	for _, tool := range tools {
		ft.Tools[tool.ToolName] = tool
	}
	e, err := New(context.Background(), Options{Registry: reg, Config: parseConfig(t, cfg), Workspace: ws, Tools: ft})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPostTasks(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\n")
	_ = os.WriteFile(filepath.Join(ws.Root, "app", "stale.txt"), []byte("old"), 0o644)
	_ = os.WriteFile(filepath.Join(ws.Root, "app", "keep.cfg"), []byte("keep"), 0o644)
	tool := gen()
	e := engineWithTools(t, ws, `fix:
  postTasks:
    - tool: gen
      args: [--all]
      files: ["app/*.txt"]
    - tool: gen
      ecosystems: [npm]
`, tool)
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangeSets) != 1 {
		t.Fatalf("plan: %+v failed=%+v", plan.ChangeSets, plan.Failed)
	}
	cs := plan.ChangeSets[0]
	files := map[string]sdk.FileChange{}
	for _, f := range cs.Files {
		files[f.Path] = f
	}
	// The task ran once, after verification rejected 1.5 and accepted 2.0.
	if got := string(files["app/generated.txt"].Content); got != "generated from\nlib=2.0\n" {
		t.Errorf("generated file follows the verified change: %q", got)
	}
	if !files["app/stale.txt"].Delete {
		t.Errorf("deleting a kept file is part of the change: %+v", files["app/stale.txt"])
	}
	if _, ok := files["app/gen.log"]; ok {
		t.Error("files outside postTasks[].files are discarded")
	}
	if _, ok := files["app/keep.cfg"]; ok {
		t.Error("deleting a file outside postTasks[].files is undone")
	}
	if len(cs.Files) != 3 || !strings.Contains(cs.Diff, "+generated from") {
		t.Errorf("files: %v\n%s", cs.Files, cs.Diff)
	}
	if len(tool.Invocations) != 1 || strings.Join(tool.Invocations[0].Args, " ") != "--all" ||
		tool.Invocations[0].Dir != filepath.Join(filepath.Dir(tool.Invocations[0].Dir), "app") {
		t.Errorf("only the maven task runs, in the project directory, once per change request: %+v", tool.Invocations)
	}
	if strings.Join(cs.Items[0].Tasks, ",") != "gen --all" {
		t.Errorf("items list the tasks: %v", cs.Items[0].Tasks)
	}
	body := Body(cs)
	if !strings.Contains(body, "### Post tasks") || !strings.Contains(body, "`gen --all`") {
		t.Errorf("body lists the post tasks:\n%s", body)
	}
	if data, _ := os.ReadFile(filepath.Join(ws.Root, "app", "stale.txt")); string(data) != "old" {
		t.Error("the real workspace must not be modified")
	}
}

func TestPostTasksOncePerChangeRequest(t *testing.T) {
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		tool := gen()
		e := engineWithTools(t, fixWorkspace(t, "lib=1.0\nother=1.0\nboth=1.0\n"), `fix:
  verify: `+mode+`
  groups:
    - name: all-g
      match: ["pkg:maven/g/*"]
  postTasks:
    - tool: gen
      files: ["app/*.txt"]
`, tool)
		plan := planFor(t, e)
		if len(plan.ChangeSets) != 1 || len(plan.ChangeSets[0].Items) != 3 {
			t.Fatalf("%s: plan: %+v failed=%+v", mode, plan.ChangeSets, plan.Failed)
		}
		if len(tool.Invocations) != 1 {
			t.Errorf("%s: one run for three changes, got %d", mode, len(tool.Invocations))
		}
		var generated string
		for _, f := range plan.ChangeSets[0].Files {
			if f.Path == "app/generated.txt" {
				generated = string(f.Content)
			}
		}
		if generated != "generated from\nlib=2.0\nother=1.1\nboth=1.9\n" {
			t.Errorf("%s: the task sees the three verified changes: %q", mode, generated)
		}
	}
}

func TestFailingPostTaskDropsTheChangeRequest(t *testing.T) {
	bad := &sdktest.FakeTool{ToolName: "bad", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if data, _ := os.ReadFile(filepath.Join(spec.Dir, "versions.txt")); !strings.Contains(string(data), "lib=2.0") {
			return sdk.RunResult{}, nil
		}
		_ = os.WriteFile(filepath.Join(spec.Dir, "half.txt"), []byte("partial"), 0o644)
		return sdk.RunResult{Tail: []string{"generator exploded"}}, errors.New("exit status 1")
	}}
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		bad.Invocations = nil
		plan := planFor(t, engineWithTools(t, fixWorkspace(t, "lib=1.0\nother=1.0\n"), "fix:\n  verify: "+mode+"\n  postTasks:\n    - tool: bad\n", bad))
		if len(plan.ChangeSets) != 1 || !strings.HasSuffix(plan.ChangeSets[0].Branch, "updates-other") || len(plan.Failed) != 1 ||
			plan.Failed[0].Reason != "post task bad failed: exit status 1: generator exploded" || plan.Failed[0].Group != "security/g:lib" {
			t.Errorf("%s: only the lib request is dropped: %+v failed=%+v", mode, plan.ChangeSets, plan.Failed)
		}
		if len(bad.Invocations) != 2 {
			t.Errorf("%s: no other alternative is tried after a failing task: %d runs", mode, len(bad.Invocations))
		}
		for _, cs := range plan.ChangeSets {
			if strings.Contains(cs.Diff, "half.txt") {
				t.Errorf("%s: a failed task's leftovers are undone:\n%s", mode, cs.Diff)
			}
		}
	}
	missing := planFor(t, engineWithTools(t, fixWorkspace(t, "lib=1.0\n"), "fix:\n  postTasks:\n    - tool: nowhere\n"))
	if len(missing.Failed) != 1 || !strings.Contains(missing.Failed[0].Reason, "post task nowhere") {
		t.Errorf("unknown tool: %+v", missing.Failed)
	}
	noTools := fixEngine(t, fixWorkspace(t, "lib=1.0\n"), "fix:\n  postTasks:\n    - tool: gen\n", true)
	if p := planFor(t, noTools); len(p.Failed) != 1 || !strings.Contains(p.Failed[0].Reason, "post task gen: tool-not-found") {
		t.Errorf("no toolchain: %+v", p.Failed)
	}
}

func planFor(t *testing.T, e *Engine) *FixPlan {
	t.Helper()
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// withMode adds fix.verify to a configuration.
func withMode(cfg, mode string) string {
	if strings.Contains(cfg, "fix:\n") {
		return strings.Replace(cfg, "fix:\n", "fix:\n  verify: "+mode+"\n", 1)
	}
	return cfg + "fix:\n  verify: " + mode + "\n"
}

func TestUnverifiedChanges(t *testing.T) {
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		t.Run(mode, func(t *testing.T) {
			testUnverifiedChanges(t, func(t *testing.T, content, cfg string) *FixPlan { return planWith(t, content, withMode(cfg, mode)) })
		})
	}
}

func testUnverifiedChanges(t *testing.T, planWith func(*testing.T, string, string) *FixPlan) {
	content := "broken=1.0\nunrelated=1.0\n"
	// By default a change after which the finding is still reported is a failure.
	plain := planWith(t, content, "")
	if len(plain.ChangeSets) != 0 || len(plain.Failed) != 1 {
		t.Fatalf("default: %+v %+v", plain.ChangeSets, plain.Failed)
	}
	plan := planWith(t, content, "fix:\n  unverified:\n    publish: true\n")
	if len(plan.ChangeSets) != 1 || len(plan.Failed) != 0 {
		t.Fatalf("unverified.publish: %+v failed=%+v", plan.ChangeSets, plan.Failed)
	}
	cs := plan.ChangeSets[0]
	it := cs.Items[0]
	if !cs.Unverified || !it.Unverified || it.VerifyFailure != "CVE-4 is still reported after the change" || it.To != "2.0" {
		t.Errorf("unverified item: %+v", it)
	}
	if !strings.HasPrefix(cs.Title, "Deepward: [unverified] ") || !strings.Contains(strings.Join(cs.Labels, ","), "deepward-unverified") {
		t.Errorf("title/labels: %q %v", cs.Title, cs.Labels)
	}
	body := Body(cs)
	for _, want := range []string{"**Not verified.**", "### Why it is not verified", "CVE-4 is still reported after the change",
		"`mvn dependency:tree -Dverbose -Dincludes=g:broken`", "### Targeted vulnerabilities"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "verified by **Deepward**") || strings.Contains(body, "### Fixed vulnerabilities") {
		t.Errorf("an unverified body must not claim a verified fix:\n%s", body)
	}
	custom := planWith(t, content, "fix:\n  unverified:\n    publish: true\n    labels: [needs-review]\n")
	if l := strings.Join(custom.ChangeSets[0].Labels, ","); !strings.HasSuffix(l, "needs-review") || strings.Contains(l, "deepward-unverified") {
		t.Errorf("custom labels: %s", l)
	}
	// A change that breaks the build is never published, even as unverified.
	verified := planWith(t, "lib=1.0\n", "fix:\n  unverified:\n    publish: true\n")
	if verified.ChangeSets[0].Unverified || verified.ChangeSets[0].Items[0].To != "2.0" {
		t.Errorf("a verified alternative wins: %+v", verified.ChangeSets[0])
	}
}

func TestInspectHints(t *testing.T) {
	it := FixItem{Findings: []sdk.Finding{{Locations: []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:npm/%40scope/pkg@1.0.0"}}}}}}
	for eco, want := range map[string]string{"npm": "`npm ls @scope/pkg`", "cargo": "`cargo tree -i @scope/pkg`", "go": "`go mod why -m @scope/pkg`",
		"nuget": "`dotnet list package --include-transitive`", "sbt": "`sbt \"whatDependsOn @scope/pkg\"`", "other": ""} {
		if got := inspectHint(sdk.Project{Ecosystem: eco}, it); got != want {
			t.Errorf("%s: %q", eco, got)
		}
	}
	if inspectHint(sdk.Project{Ecosystem: "maven"}, FixItem{}) != "" {
		t.Error("no package, no hint")
	}
}

func TestGlobs(t *testing.T) {
	cases := map[string]map[string]bool{
		"**/*.lock":       {"a.lock": true, "x/y/a.lock": true, "a.locks": false},
		"app/*.txt":       {"app/a.txt": true, "app/b/a.txt": false},
		"docs/**":         {"docs/a.md": true, "docs/x/y.md": true, "doc/a.md": false},
		"src/**/gen/?.go": {"src/gen/a.go": true, "src/x/y/gen/b.go": true, "src/gen/ab.go": false},
		"/abs/file":       {"abs/file": true},
	}
	for pattern, paths := range cases {
		re, err := globRegexp(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for p, want := range paths {
			if re.MatchString(p) != want {
				t.Errorf("%s ~ %s: want %v", pattern, p, want)
			}
		}
	}
	if _, err := compileGlobs([]string{" "}); err == nil {
		t.Error("empty pattern")
	}
	var c Config
	if err := decodeConfigForTest("fix:\n  postTasks:\n    - args: [x]\n    - tool: gen\n      files: ['']\n", &c); err != nil {
		t.Fatal(err)
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "fix.postTasks[0].tool is required") || !strings.Contains(err.Error(), "fix.postTasks[1].files") {
		t.Errorf("validation: %v", err)
	}
	if (PostTask{Tool: "mvn", Args: []string{"-q", "generate-sources"}}).String() != "mvn -q generate-sources" {
		t.Error("String")
	}
	if !(PostTask{Projects: []string{"./app"}}).appliesTo(sdk.Project{Dir: "app"}) || (PostTask{Projects: []string{"web"}}).appliesTo(sdk.Project{Dir: "app"}) {
		t.Error("projects filter")
	}
}
