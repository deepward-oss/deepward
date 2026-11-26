package cargo

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

// cargoTool rewrites the locked version on `cargo update -p name@from --precise to`.
func cargoTool() *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "cargo", ToolVersion: "1.100.0", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		if spec.Args[0] != "update" {
			return sdk.RunResult{}, nil
		}
		name, from, _ := strings.Cut(spec.Args[2], "@")
		to := spec.Args[4]
		p := filepath.Join(spec.Dir, "Cargo.lock")
		b, _ := os.ReadFile(p)
		old := "name = \"" + name + "\"\nversion = \"" + from + "\""
		if !strings.Contains(string(b), old) {
			return sdk.RunResult{Tail: []string{"error: package ID specification did not match"}}, &sdk.ExitError{Tool: "cargo", ExitCode: 101,
				Tail: []string{"error: failed to select a version for the requirement"}}
		}
		return sdk.RunResult{}, os.WriteFile(p, []byte(strings.Replace(string(b), old, "name = \""+name+"\"\nversion = \""+to+"\"", 1)), 0o644)
	}}
}

func run(t *testing.T, p *Plugin, ws *sdk.Workspace, project sdk.Project, changes ...sdk.PackageChange) sdk.RealizeResult {
	t.Helper()
	res, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func edit(res sdk.RealizeResult, file string) string {
	for _, e := range res.Edits {
		if e.Path == file {
			return e.Replacement
		}
	}
	return ""
}

func TestRealizeLockOnly(t *testing.T) {
	tool := cargoTool()
	p := plugin(t, tool)
	ws, project := workspace(t)
	res := run(t, p, ws, project, sdk.PackageChange{PURL: "pkg:cargo/regex", From: "1.5.4", To: "1.5.6"})
	if len(res.Edits) != 1 || res.Edits[0].Path != "Cargo.lock" || !strings.Contains(res.Edits[0].Replacement, "name = \"regex\"\nversion = \"1.5.6\"") {
		t.Fatalf("lock-only: %+v", res)
	}
	if got := strings.Join(tool.Invocations[0].Args, " "); got != "update -p regex@1.5.4 --precise 1.5.6" || tool.Invocations[0].Dir != ws.Root {
		t.Errorf("args: %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(ws.Root, "Cargo.lock")); strings.Contains(string(b), "1.5.6") {
		t.Error("workspace must be restored")
	}
}

func TestRealizeBump(t *testing.T) {
	p := plugin(t, cargoTool())
	ws, project := workspace(t)
	res := run(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:cargo/smallvec", From: "1.6.0", To: "1.6.1"},
		sdk.PackageChange{PURL: "pkg:cargo/itoa", From: "1.0.19", To: "2.0.0"})
	if app := edit(res, "app/Cargo.toml"); !strings.Contains(app, `smallvec = "=1.6.1"`) {
		t.Errorf("app/Cargo.toml:\n%s", app)
	}
	if root := edit(res, "Cargo.toml"); !strings.Contains(root, `itoa = "2.0.0"`) {
		t.Errorf("workspace dependency:\n%s", root)
	}
	if lock := edit(res, "Cargo.lock"); !strings.Contains(lock, "version = \"1.6.1\"") || !strings.Contains(lock, "version = \"2.0.0\"") {
		t.Errorf("lock:\n%s", lock)
	}
}

func TestRealizePinAndManual(t *testing.T) {
	p := plugin(t, cargoTool())
	ws, project := workspace(t)
	res := run(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:cargo/memchr", From: "2.8.3", To: "2.8.4", Pin: true},
		sdk.PackageChange{PURL: "pkg:cargo/memchr", From: "9.9.9", To: "10.0.0", Pin: true},
		sdk.PackageChange{PURL: "pkg:cargo/unknown", From: "1.0.0", To: "1.0.1"},
		sdk.PackageChange{PURL: "bad", To: "1"})
	if lock := edit(res, "Cargo.lock"); !strings.Contains(lock, "name = \"memchr\"\nversion = \"2.8.4\"") {
		t.Errorf("pin:\n%s", lock)
	}
	if len(res.Manual) != 3 || !strings.Contains(res.Manual[0].Reason, "do not allow 10.0.0") || !strings.Contains(res.Manual[0].Reason, "failed to select") ||
		!strings.Contains(res.Manual[1].Reason, "not declared") || res.Manual[2].Reason != "invalid package URL" {
		t.Errorf("manual: %+v", res.Manual)
	}
	// A requirement that cannot be rewritten.
	appToml := filepath.Join(ws.Root, "app", "Cargo.toml")
	b, _ := os.ReadFile(appToml)
	_ = os.WriteFile(appToml, []byte(strings.Replace(string(b), `regex = "1.5"`, `regex = ">=1.5, <1.6"`, 1)), 0o644)
	res = run(t, p, ws, project, sdk.PackageChange{PURL: "pkg:cargo/regex", From: "1.5.4", To: "1.6.0"})
	if len(res.Manual) != 1 || !strings.Contains(res.Manual[0].Reason, "several comparators") || res.Manual[0].Change.To != "1.6.0" {
		t.Errorf("unrewritable: %+v", res)
	}
	// Direct update failure is an error.
	res2, err := p.Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws,
		Changes: []sdk.PackageChange{{PURL: "pkg:cargo/hex", From: "0.0.1", To: "0.4.4"}}})
	if err == nil || !strings.Contains(err.Error(), "failed to select") {
		t.Errorf("direct failure: %+v %v", res2, err)
	}
}

func TestRealizeWithoutLock(t *testing.T) {
	tool := cargoTool()
	p := plugin(t, tool)
	ws, project := workspace(t)
	_ = os.Remove(filepath.Join(ws.Root, "Cargo.lock"))
	project.Attributes["lockfile"] = ""
	res := run(t, p, ws, project,
		sdk.PackageChange{PURL: "pkg:cargo/smallvec", From: "1.6.0", To: "1.6.1"},
		sdk.PackageChange{PURL: "pkg:cargo/regex", From: "1.5.4", To: "1.5.6"},
		sdk.PackageChange{PURL: "pkg:cargo/memchr", From: "2.8.3", To: "2.8.4", Pin: true})
	if len(res.Edits) != 1 || res.Edits[0].Path != "app/Cargo.toml" || len(res.Manual) != 2 || len(tool.Invocations) != 0 {
		t.Errorf("no lock: %+v", res)
	}
}

func TestRealizeErrors(t *testing.T) {
	ws, project := workspace(t)
	change := sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{{PURL: "pkg:cargo/regex", From: "1.5.4", To: "1.5.6"}}}
	if _, err := plugin(t, nil).Realize(context.Background(), change); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("no cargo: %v", err)
	}
	if _, err := New().Realize(context.Background(), change); err == nil {
		t.Error("not initialized")
	}
	if tailError(errors.New("x")).Error() != "x" {
		t.Error("plain error")
	}
	long := &sdktest.FakeTool{ToolName: "cargo", RunFunc: func(context.Context, sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, &sdk.ExitError{Tool: "cargo", ExitCode: 101, Tail: append(make([]string, 12), "last")}
	}}
	if _, err := plugin(t, long).Realize(context.Background(), change); err == nil || !strings.Contains(err.Error(), "last") {
		t.Errorf("tail: %v", err)
	}
	_ = os.WriteFile(filepath.Join(ws.Root, "app", "Cargo.toml"), []byte("[package"), 0o644)
	if _, err := plugin(t, cargoTool()).Realize(context.Background(), change); err == nil {
		t.Error("broken manifest")
	}
	if got := dedupe([]reqEdit{{file: "a"}, {file: "a"}}); len(got) != 1 {
		t.Error("dedupe")
	}
	if indexPath("Serde") != "se/rd/serde" {
		t.Error("index path")
	}
}
