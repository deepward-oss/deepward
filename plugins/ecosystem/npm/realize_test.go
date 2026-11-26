package npm

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

// rewriter is a fake package manager that rewrites strings in the lockfile
// of the directory it runs in, per command.
func rewriter(name, version, lockName string, byCommand map[string][2]string) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: name, ToolVersion: version, RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		r, ok := byCommand[spec.Args[0]]
		if !ok {
			return sdk.RunResult{}, nil
		}
		p := filepath.Join(spec.Dir, lockName)
		b, err := os.ReadFile(p)
		if err != nil {
			return sdk.RunResult{}, err
		}
		return sdk.RunResult{}, os.WriteFile(p, []byte(strings.ReplaceAll(string(b), r[0], r[1])), 0o644)
	}}
}

func realize(t *testing.T, p *Plugin, ws *sdk.Workspace, project sdk.Project, changes ...sdk.PackageChange) sdk.RealizeResult {
	t.Helper()
	res, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func edited(t *testing.T, res sdk.RealizeResult, file string) string {
	t.Helper()
	for _, e := range res.Edits {
		if e.Path == file {
			if e.Start != 0 || e.End != len(e.Expected) {
				t.Errorf("%s: not a full-file edit", file)
			}
			return e.Replacement
		}
	}
	return ""
}

func TestRealizeDirect(t *testing.T) {
	tool := rewriter("npm", "10.9.0", "package-lock.json", map[string][2]string{"install": {`"axios": "^0.21.0"`, `"axios": "^0.21.2"`}})
	p := newPlugin(t, tool)
	ws, project := variant(t, p, "npm")
	original, _ := os.ReadFile(filepath.Join(ws.Root, "package.json"))
	res := realize(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:npm/axios", From: "0.21.4", To: "0.21.2"},
		sdk.PackageChange{PURL: "pkg:npm/minimist", From: "1.2.5", To: "1.2.6"},
		sdk.PackageChange{PURL: "pkg:npm/ms", From: "2.1.3", To: "2.1.4"})
	if len(res.Manual) != 0 || len(res.Edits) != 3 {
		t.Fatalf("result: %+v", res)
	}
	pj := edited(t, res, "package.json")
	if !strings.Contains(pj, `"axios": "^0.21.2"`) || !strings.Contains(pj, `"minimist": "1.2.6"`) || len(pj) != len(original) {
		t.Errorf("package.json:\n%s", pj)
	}
	if lib := edited(t, res, "packages/lib/package.json"); !strings.Contains(lib, `"ms": "^2.1.4"`) {
		t.Errorf("workspace member:\n%s", lib)
	}
	if lock := edited(t, res, "package-lock.json"); !strings.Contains(lock, `"axios": "^0.21.2"`) {
		t.Error("lockfile was not regenerated")
	}
	if args := strings.Join(tool.Invocations[0].Args, " "); args != "install --package-lock-only --ignore-scripts --no-audit --no-fund" || tool.Invocations[0].Dir != ws.Root {
		t.Errorf("invocation: %s in %s", args, tool.Invocations[0].Dir)
	}
	if now, _ := os.ReadFile(filepath.Join(ws.Root, "package.json")); string(now) != string(original) {
		t.Error("workspace must be restored")
	}
}

func TestRealizeManual(t *testing.T) {
	tool := &sdktest.FakeTool{ToolName: "npm"}
	p := newPlugin(t, tool)
	ws, project := variant(t, p, "npm")
	pj := filepath.Join(ws.Root, "package.json")
	b, _ := os.ReadFile(pj)
	_ = os.WriteFile(pj, []byte(strings.Replace(string(b), `"lodash": "4.17.20"`, `"lodash": "^4 || ^5"`, 1)), 0o644)
	res := realize(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:npm/lodash", From: "4.17.20", To: "4.17.21"},
		sdk.PackageChange{PURL: "pkg:npm/unknown", From: "1", To: "2"},
		sdk.PackageChange{PURL: "bad", To: "2"})
	if len(res.Manual) != 3 || len(res.Edits) != 0 || len(tool.Invocations) != 0 {
		t.Fatalf("result: %+v, invocations %d", res, len(tool.Invocations))
	}
	if !strings.Contains(res.Manual[0].Reason, "cannot be rewritten") || res.Manual[0].Suggestion == "" || !strings.Contains(res.Manual[1].Reason, "not declared") {
		t.Errorf("reasons: %+v", res.Manual)
	}
}

func TestRealizePinNPM(t *testing.T) {
	// npm update fixes the lockfile within the declared ranges: no override.
	tool := rewriter("npm", "10.9.0", "package-lock.json", map[string][2]string{"update": {"follow-redirects-1.16.3", "follow-redirects-1.16.4"}})
	tool.RunFunc = chain(tool.RunFunc, func(spec sdk.RunSpec) {
		if spec.Args[0] == "update" {
			p := filepath.Join(spec.Dir, "package-lock.json")
			b, _ := os.ReadFile(p)
			_ = os.WriteFile(p, []byte(strings.Replace(string(b), `"version": "1.16.3"`, `"version": "1.16.4"`, 1)), 0o644)
		}
	})
	p := newPlugin(t, tool)
	ws, project := variant(t, p, "npm")
	pin := sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.4", Pin: true}
	res := realize(t, p, ws, project, pin)
	if len(res.Edits) != 1 || res.Edits[0].Path != "package-lock.json" || !strings.Contains(res.Edits[0].Replacement, "follow-redirects-1.16.4") {
		t.Fatalf("lockfile-only pin: %+v", res)
	}
	if got := strings.Join(tool.Invocations[0].Args, " "); got != "update follow-redirects --package-lock-only --ignore-scripts --no-audit --no-fund" {
		t.Errorf("update args: %s", got)
	}
	// npm update leaves the version: an override limited to it is added.
	tool2 := &sdktest.FakeTool{ToolName: "npm"}
	p2 := newPlugin(t, tool2)
	ws2, project2 := variant(t, p2, "npm")
	res = realize(t, p2, ws2, project2, pin)
	if pj := edited(t, res, "package.json"); !strings.Contains(pj, "\"overrides\": {\n    \"follow-redirects@1.16.3\": \"1.16.4\"\n  }") {
		t.Errorf("override:\n%s", pj)
	}
	if len(tool2.Invocations) != 2 || tool2.Invocations[1].Args[0] != "install" {
		t.Errorf("invocations: %+v", tool2.Invocations)
	}
	// npm update fails.
	failing := &sdktest.FakeTool{ToolName: "npm", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "npm", ExitCode: 1, Tail: strings.Split(strings.Repeat("line\n", 12)+"ERESOLVE", "\n")}
	}}
	p3 := newPlugin(t, failing)
	ws3, project3 := variant(t, p3, "npm")
	_, err := p3.Realize(context.Background(), sdk.RealizeRequest{Project: project3, Workspace: ws3, Changes: []sdk.PackageChange{pin}})
	if err == nil || !strings.Contains(err.Error(), "ERESOLVE") || strings.Count(err.Error(), "line") > 10 {
		t.Errorf("update failure: %v", err)
	}
	// Broken lockfile after the update.
	breaker := &sdktest.FakeTool{ToolName: "npm", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, os.WriteFile(filepath.Join(spec.Dir, "package-lock.json"), []byte("{"), 0o644)
	}}
	p4 := newPlugin(t, breaker)
	ws4, project4 := variant(t, p4, "npm")
	if _, err := p4.Realize(context.Background(), sdk.RealizeRequest{Project: project4, Workspace: ws4, Changes: []sdk.PackageChange{pin}}); err == nil {
		t.Error("broken lockfile after update")
	}
}

func chain(first func(context.Context, sdk.RunSpec) (sdk.RunResult, error), then func(sdk.RunSpec)) func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
	return func(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		res, err := first(ctx, spec)
		then(spec)
		return res, err
	}
}

func TestRealizePinPNPMAndYarn(t *testing.T) {
	pnpm := &sdktest.FakeTool{ToolName: "pnpm", ToolVersion: "9.15.0"}
	p := newPlugin(t, pnpm)
	ws, project := variant(t, p, "pnpm9")
	pin := sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.4", Pin: true}
	res := realize(t, p, ws, project, pin)
	if pj := edited(t, res, "package.json"); !strings.Contains(pj, "\"pnpm\": {\n    \"overrides\": {\n      \"follow-redirects@1.16.3\": \"1.16.4\"") {
		t.Errorf("pnpm override:\n%s", pj)
	}
	if got := strings.Join(pnpm.Invocations[0].Args, " "); got != "install --lockfile-only --ignore-scripts --no-frozen-lockfile" {
		t.Errorf("pnpm args: %s", got)
	}
	yarn := &sdktest.FakeTool{ToolName: "yarn", ToolVersion: "1.22.22"}
	p = newPlugin(t, yarn)
	ws, project = variant(t, p, "yarn1")
	res = realize(t, p, ws, project, pin)
	if pj := edited(t, res, "package.json"); !strings.Contains(pj, `"resolutions": {`) || !strings.Contains(pj, `"follow-redirects": "1.16.4"`) {
		t.Errorf("yarn resolution:\n%s", pj)
	}
	// Another major of the package is installed: manual.
	res = realize(t, p, ws, project, sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "2.0.0", Pin: true})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Reason, "also installed at 1.16.3") {
		t.Errorf("yarn majors: %+v", res)
	}
	res = realize(t, p, ws, project, sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.2", Pin: true})
	if len(res.Manual) != 1 {
		t.Errorf("yarn downgrade: %+v", res)
	}
	_ = os.WriteFile(filepath.Join(ws.Root, "yarn.lock"), []byte("broken\n"), 0o644)
	if _, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{pin}}); err == nil {
		t.Error("broken yarn.lock")
	}
	_ = os.Remove(filepath.Join(ws.Root, "yarn.lock"))
	if _, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{pin}}); err == nil {
		t.Error("missing yarn.lock")
	}
}

func TestRealizeEdgeCases(t *testing.T) {
	// No lockfile: package.json is edited, nothing runs, pins are manual.
	tool := &sdktest.FakeTool{ToolName: "npm"}
	p := newPlugin(t, tool)
	ws, project := variant(t, p, "npm6")
	_ = os.Remove(filepath.Join(ws.Root, "package-lock.json"))
	project.Attributes["lockfile"] = ""
	res := realize(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:npm/lodash", From: "4.17.20", To: "4.17.21"},
		sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.4", Pin: true})
	if len(res.Edits) != 1 || len(res.Manual) != 1 || len(tool.Invocations) != 0 {
		t.Errorf("no lockfile: %+v", res)
	}
	// Unknown package manager, missing tool, broken package.json, override target not an object.
	ws, project = variant(t, p, "npm")
	project.Attributes["packageManager"] = "bun"
	res = realize(t, p, ws, project, sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.4", Pin: true})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Reason, "unknown package manager") {
		t.Errorf("unknown manager: %+v", res)
	}
	noTool := newPlugin(t)
	ws, project = variant(t, noTool, "npm")
	_, err := noTool.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:npm/lodash", From: "4.17.20", To: "4.17.21"}}})
	if !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no tool: %v", err)
	}
	if _, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:npm/lodash", To: "4.17.21"}}}); err == nil {
		t.Error("not initialized")
	}
	pnpm := newPlugin(t, &sdktest.FakeTool{ToolName: "pnpm"})
	ws, project = variant(t, pnpm, "pnpm9")
	pj := filepath.Join(ws.Root, "package.json")
	b, _ := os.ReadFile(pj)
	_ = os.WriteFile(pj, []byte(strings.Replace(string(b), "{", `{"pnpm": "x",`, 1)), 0o644)
	res = realize(t, pnpm, ws, project, sdk.PackageChange{PURL: "pkg:npm/follow-redirects", From: "1.16.3", To: "1.16.4", Pin: true})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Reason, "not an object") {
		t.Errorf("pnpm not an object: %+v", res)
	}
	_ = os.WriteFile(pj, []byte("{"), 0o644)
	if _, err := pnpm.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:npm/x", To: "1"}}}); err == nil {
		t.Error("broken package.json")
	}
	// Lockfile regeneration fails.
	failing := &sdktest.FakeTool{ToolName: "npm", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "npm", ExitCode: 1, Tail: []string{"ETARGET No matching version"}}
	}}
	pf := newPlugin(t, failing)
	ws, project = variant(t, pf, "npm")
	_, err = pf.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:npm/lodash", From: "4.17.20", To: "4.17.21"}}})
	if err == nil || !strings.Contains(err.Error(), "ETARGET") {
		t.Errorf("regeneration failure: %v", err)
	}
	if toolError(errors.New("plain")).Error() != "plain" {
		t.Error("plain error")
	}
}
