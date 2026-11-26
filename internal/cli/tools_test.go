package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// TestHelperProcess is the fake external tool for the tests in this file.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch {
	case len(args) > 0 && args[0] == "-v":
		fmt.Println("Apache Maven 3.9.9")
	case len(args) > 2 && args[0] == "scan":
		sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[{"ruleId":"xxe","level":"error","message":{"text":"XXE"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"api/Main.java"}}}]}]}]}`
		_ = os.WriteFile(args[2], []byte(sarif), 0o600)
		os.Exit(1) // semgrep exits 1 when it finds something
	}
	os.Exit(0)
}

type requirer struct {
	sdktest.FakeEcosystem
	reqs []sdk.ToolRequirement
}

func (r *requirer) ToolRequirements() []sdk.ToolRequirement { return r.reqs }

type pluginRequirer struct{ sdktest.FakeAnalyzer }

func (*pluginRequirer) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: "git"}}
}

func helperCmd() []string { return []string{os.Args[0], "-test.run=TestHelperProcess", "--"} }

func toolHarness(t *testing.T, extra ...sdk.Plugin) *harness {
	t.Helper()
	resolver := &sdktest.FakeToolResolver{ID: "path", Commands: map[string][]string{"mvn": helperCmd(), "semgrep": helperCmd(), "git": helperCmd()}}
	plugins := append([]sdk.Plugin{
		resolver,
		&requirer{FakeEcosystem: sdktest.FakeEcosystem{ID: "sbt", Marker: "build.sbt"}, reqs: []sdk.ToolRequirement{{Name: "sbt", Constraint: ">=1.4"}}},
		&requirer{FakeEcosystem: sdktest.FakeEcosystem{ID: "mvn-eco", Marker: "pom.xml"}, reqs: []sdk.ToolRequirement{{Name: "mvn"}}},
		&sdktest.FakeOutputFormat{ID: "sarif-format", Format: "sarif"},
	}, extra...)
	h := newHarness(t, plugins...)
	h.write("backend/build.sbt", "")
	h.write("deepward.yaml", `tools:
  mvn:
    env: {GO_WANT_HELPER: "1"}
  semgrep:
    env: {GO_WANT_HELPER: "1"}
  git:
    env: {GO_WANT_HELPER: "1"}
    resolvers: [path]
`)
	return h
}

func (h *harness) runWithTools(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	env := Env{Args: args, Stdout: &h.stdout, Stderr: &h.stderr, Cwd: h.dir, Registry: h.reg,
		Getenv: func(k string) string { return h.env[k] }, Environ: func() []string { return []string{"PATH=" + os.Getenv("PATH"), "DEEPWARD_GITLAB_TOKEN=glpat-secret"} },
		NewTools: DefaultTools}
	return Run(t0(), env)
}

func TestDoctor(t *testing.T) {
	h := toolHarness(t, &pluginRequirer{sdktest.FakeAnalyzer{ID: "needs-git"}})
	h.write("deepward.yaml", `tools:
  mvn:
    env: {GO_WANT_HELPER: "1"}
  git:
    env: {GO_WANT_HELPER: "1"}
    versionArgs: [-v]
    versionPattern: 'Apache Maven (\S+)'
projects:
  api:
    setup:
      - {tool: mvn, args: [install]}
`)
	code := h.runWithTools("doctor")
	out := h.stdout.String()
	if code != ExitRuntime || !strings.Contains(h.stderr.String(), "not satisfied") {
		t.Fatalf("exit %d:\n%s\n%s", code, out, h.stderr.String())
	}
	for _, want := range []string{"mvn", "mvn-eco:api", "3.9.9", "sbt >=1.4", "sbt:backend", "tool-not-found", "needs-git", "setup", "sbt for sbt:backend: config-path: resolver not available"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	// Without the sbt project every requirement is satisfied.
	_ = os.RemoveAll(filepath.Join(h.dir, "backend"))
	if code := h.runWithTools("doctor"); code != ExitOK {
		t.Errorf("all resolved: exit %d\n%s%s", code, h.stdout.String(), h.stderr.String())
	}
	empty := newHarness(t)
	empty.reg = sdk.NewRegistry()
	if code := empty.runWithTools("doctor"); code != ExitOK || !strings.Contains(empty.stdout.String(), "no enabled plugin requires") {
		t.Errorf("nothing required: %d %s", code, empty.stdout.String())
	}
	noTools := newHarness(t)
	if code := noTools.run("doctor"); code != ExitRuntime {
		t.Errorf("no toolchain: %d", code)
	}
}

func TestIntegrationsThroughCLI(t *testing.T) {
	h := toolHarness(t)
	h.write("deepward.yaml", `tools:
  semgrep:
    env: {GO_WANT_HELPER: "1"}
integrations:
  semgrep:
    tool: semgrep
    args: [scan, --output, "{{.OutputFile}}"]
    output: sarif
    exitCodes: [0, 1]
    ecosystems: [mvn-eco]
`)
	if code := h.runWithTools("scan", "--format", "json", "--allow-partial"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var res sdk.ScanResult
	data, _ := os.ReadFile(filepath.Join(h.dir, "deepward-out", "report.json"))
	_ = json.Unmarshal(data, &res)
	n := 0
	for _, f := range res.Findings {
		if f.Analyzer == "semgrep" && f.Tool == "semgrep" && f.Rule == "xxe" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("semgrep findings for the two maven projects: %d\n%s", n, data)
	}
	h.runWithTools("plugins", "list")
	if !strings.Contains(h.stdout.String(), "[integration]") {
		t.Errorf("plugins list:\n%s", h.stdout.String())
	}
	h.write("deepward.yaml", "integrations:\n  semgrep:\n    tool: semgrep\n    output: spdx\n")
	if code := h.runWithTools("config", "validate"); code != ExitUsage || !strings.Contains(h.stderr.String(), `integrations.semgrep: unknown output format "spdx"`) {
		t.Errorf("unknown format: %d %s", code, h.stderr.String())
	}
	h.write("deepward.yaml", "integrations:\n  path:\n    tool: semgrep\n    output: sarif\n")
	if code := h.runWithTools("config", "validate"); code != ExitUsage {
		t.Errorf("clashing ID: %d", code)
	}
	h.write("deepward.yaml", "tools:\n  semgrep:\n    download: {url: https://x/semgrep.zip}\n")
	if code := h.runWithTools("config", "validate"); code != ExitUsage || !strings.Contains(h.stderr.String(), "sha256 is required") {
		t.Errorf("download without checksum: %d %s", code, h.stderr.String())
	}
	h.write("deepward.yaml", "plugins:\n  path: {enabled: false}\n")
	if code := h.runWithTools("config", "validate"); code != ExitOK {
		t.Errorf("disabled resolver: %d %s", code, h.stderr.String())
	}
	dup := sdk.NewRegistry()
	if _, err := runRegistry(dup, engineConfigWithIntegrations(t), "x"); err == nil {
		t.Error("integration needs an output format")
	}
}

func t0() context.Context { return context.Background() }

func engineConfigWithIntegrations(t *testing.T) engine.Config {
	t.Helper()
	cfg, _, err := loadConfigFromString(t, "integrations:\n  x:\n    tool: t\n    output: sarif\n")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadConfigFromString(t *testing.T, src string) (engine.Config, string, error) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "deepward.yaml"), []byte(src), 0o600)
	return loadConfig(dir, "")
}

func TestRelTo(t *testing.T) {
	if relTo("/repo", "/repo/mvnw") != "mvnw" || relTo("/repo", "/usr/bin/mvn") != "/usr/bin/mvn" || relTo("/repo", "fake:x") != "fake:x" {
		t.Error("relTo")
	}
}
