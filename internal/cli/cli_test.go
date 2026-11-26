package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

type jsonReporter struct{ sdktest.FakeReporter }

func (r *jsonReporter) Render(_ context.Context, res *sdk.ScanResult, w io.Writer) error {
	return json.NewEncoder(w).Encode(res)
}

type updater struct {
	sdktest.FakeAnalyzer
	err error
}

func (u *updater) UpdateData(context.Context) (string, error) {
	return "downloaded 2 ecosystems", u.err
}

type harness struct {
	t      *testing.T
	dir    string
	env    map[string]string
	stdout bytes.Buffer
	stderr bytes.Buffer
	reg    *sdk.Registry
	tty    bool
}

func newHarness(t *testing.T, plugins ...sdk.Plugin) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), env: map[string]string{}, reg: sdk.NewRegistry()}
	h.write("api/pom.xml", "<project/>")
	h.write("web/pom.xml", "<project/>")
	defaults := []sdk.Plugin{
		&sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml"},
		&sdktest.FakeReporter{ID: "html", Name: "html"},
		&jsonReporter{sdktest.FakeReporter{ID: "json", Name: "json"}},
		&sdktest.FakeReporter{ID: "sarif", Name: "sarif"},
	}
	override := map[string]bool{}
	for _, p := range plugins {
		override[p.Info().ID] = true
	}
	for _, p := range append(defaults, plugins...) {
		if override[p.Info().ID] && !containsPlugin(plugins, p) {
			continue
		}
		if err := h.reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *harness) write(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return Run(context.Background(), Env{
		Args: args, Stdout: &h.stdout, Stderr: &h.stderr, Cwd: h.dir, Registry: h.reg,
		Getenv: func(k string) string { return h.env[k] }, Clock: sdk.FixedClock{T: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)},
		StdoutIsTTY: h.tty, Version: "1.2.3", Commit: "abc", Date: "2026-11-26",
	})
}

func (h *harness) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(h.dir, filepath.FromSlash(rel)))
	return err == nil
}

func analyzer(sev sdk.Severity) *sdktest.FakeAnalyzer {
	return &sdktest.FakeAnalyzer{ID: "vulns", Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		f := sdktest.SimpleFinding("", "CVE-1", sev, "")
		f.Kind = sdk.KindVulnerability
		return []sdk.Finding{f}, nil
	}}
}

func TestDefaultScan(t *testing.T) {
	h := newHarness(t, analyzer(sdk.SeverityHigh))
	if code := h.run("scan"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !h.exists("deepward-out/report.html") || !h.exists("deepward-out/report.json") || h.exists("deepward-out/report.sarif") {
		t.Error("default formats html+json")
	}
	out := h.stdout.String()
	for _, want := range []string{"Deepward 1.2.3: 2 project(s) scanned (2 ok, 0 skipped, 0 failed)", "0 critical, 2 high", "vulnerability: 2", "Reports: deepward-out/report.html, deepward-out/report.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("no ANSI when stdout is not a TTY")
	}
}

func TestFormatsAndOutDir(t *testing.T) {
	h := newHarness(t)
	if code := h.run("scan", "--format", "html,sarif", "--out", "build/reports"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !h.exists("build/reports/report.html") || !h.exists("build/reports/report.sarif") || h.exists("build/reports/report.json") {
		t.Error("selected formats")
	}
	if code := h.run("scan", "--format", "pdf"); code != ExitUsage || !strings.Contains(h.stderr.String(), `unknown format "pdf"`) {
		t.Errorf("unknown format: %d %s", code, h.stderr.String())
	}
	h.env["DEEPWARD_FORMAT"] = "json"
	h.env["DEEPWARD_OUT"] = "env-out"
	if code := h.run("scan"); code != ExitOK || !h.exists("env-out/report.json") || h.exists("env-out/report.html") {
		t.Errorf("env formats: %d", code)
	}
}

func TestUnknownCommandAndUsage(t *testing.T) {
	h := newHarness(t)
	if code := h.run("scna"); code != ExitUsage || !strings.Contains(h.stderr.String(), "scan") {
		t.Errorf("unknown command: %d %q", code, h.stderr.String())
	}
	if code := h.run("scan", "--bogus"); code != ExitUsage {
		t.Errorf("unknown flag: %d", code)
	}
	if code := h.run("scan", "missing-dir"); code != ExitUsage {
		t.Errorf("missing root: %d", code)
	}
	if code := h.run("--help"); code != ExitOK || !strings.Contains(h.stdout.String(), "scan") {
		t.Errorf("help: %d", code)
	}
	for _, cmd := range [][]string{{"scan", "--help"}, {"fix", "--help"}, {"report", "--help"}, {"db", "update", "--help"},
		{"plugins", "list", "--help"}, {"doctor", "--help"}, {"config", "validate", "--help"}, {"version", "--help"}} {
		if code := h.run(cmd...); code != ExitOK {
			t.Errorf("%v: %d", cmd, code)
		}
	}
	if code := h.run("scan", "--log-format", "xml"); code != ExitUsage {
		t.Errorf("bad log format: %d", code)
	}
}

func TestConfigPrecedence(t *testing.T) {
	h := newHarness(t, analyzer(sdk.SeverityHigh))
	h.write("deepward.yaml", "failOn: medium\n")
	if code := h.run("scan"); code != ExitGate {
		t.Errorf("file threshold medium, high finding: exit %d", code)
	}
	if code := h.run("scan", "--fail-on", "critical"); code != ExitOK {
		t.Errorf("flag overrides file: exit %d", code)
	}
	h.env["DEEPWARD_FAIL_ON"] = "critical"
	if code := h.run("scan"); code != ExitOK {
		t.Errorf("env overrides file: exit %d", code)
	}
	if code := h.run("scan", "--fail-on", "high"); code != ExitGate {
		t.Errorf("flag overrides env: exit %d", code)
	}
	h.env["DEEPWARD_FAIL_ON"] = "bogus"
	if code := h.run("scan"); code != ExitUsage {
		t.Errorf("bad env severity: exit %d", code)
	}
}

func TestConfigErrors(t *testing.T) {
	h := newHarness(t)
	h.write("deepward.yaml", "failon: high\n")
	if code := h.run("scan"); code != ExitUsage || !strings.Contains(h.stderr.String(), `deepward.yaml:1: failon: unknown key "failon"`) {
		t.Errorf("typo: %d %s", code, h.stderr.String())
	}
	h.write("deepward.yaml", "failOn: [")
	if code := h.run("scan"); code != ExitUsage {
		t.Errorf("syntax: %d", code)
	}
	if code := h.run("scan", "--config", "nope.yaml"); code != ExitUsage {
		t.Errorf("missing explicit config: %d", code)
	}
	h.write("deepward.yaml", "plugins:\n  gradle: {enabled: true}\n")
	if code := h.run("config", "validate"); code != ExitUsage || !strings.Contains(h.stderr.String(), "unknown plugin") {
		t.Errorf("unknown plugin: %d %s", code, h.stderr.String())
	}
	_ = os.Remove(filepath.Join(h.dir, "deepward.yaml"))
	h.write("deepward.yml", "failOn: low\n")
	if code := h.run("config", "validate"); code != ExitOK || !strings.Contains(h.stdout.String(), "deepward.yml") {
		t.Errorf("yml fallback: %d %s", code, h.stdout.String())
	}
}

func TestConfigValidateMasksSecrets(t *testing.T) {
	h := newHarness(t)
	h.write("deepward.yaml", `failOn: high
tools:
  mvn:
    env:
      NEXUS_TOKEN: s3cr3t
      NEXUS_USER: ci
      NEXUS_PASSWORD: ${env:NEXUS_PASSWORD}
`)
	if code := h.run("config", "validate"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	if strings.Contains(out, "s3cr3t") || !strings.Contains(out, "NEXUS_TOKEN: '***'") && !strings.Contains(out, `NEXUS_TOKEN: "***"`) {
		t.Errorf("secret not masked:\n%s", out)
	}
	if !strings.Contains(out, "NEXUS_USER: ci") || !strings.Contains(out, "${env:NEXUS_PASSWORD}") || !strings.Contains(out, "failOn: high") {
		t.Errorf("effective config:\n%s", out)
	}
	_ = os.Remove(filepath.Join(h.dir, "deepward.yaml"))
	if code := h.run("config", "validate"); code != ExitOK || !strings.Contains(h.stdout.String(), "defaults") {
		t.Errorf("no file: %s", h.stdout.String())
	}
}

func TestExitCodes(t *testing.T) {
	sup := newHarness(t, analyzer(sdk.SeverityHigh))
	sup.write("deepward.yaml", "failOn: high\nignore:\n  - {id: CVE-1, reason: not used}\n")
	if code := sup.run("scan"); code != ExitOK || !strings.Contains(sup.stdout.String(), "suppressed") || !strings.Contains(sup.stdout.String(), "passed") {
		t.Errorf("suppressed: %d\n%s", code, sup.stdout.String())
	}
	failing := &sdktest.FakeEcosystem{ID: "sbt", Marker: "build.sbt", Facts: map[string]func(context.Context, sdk.FactRequest) (any, error){
		"dependency-graph": func(context.Context, sdk.FactRequest) (any, error) { return nil, errors.New("resolution failed") },
	}}
	needs := &sdktest.FakeAnalyzer{ID: "needs", Needs: []string{"dependency-graph"}}
	h := newHarness(t, failing, needs, analyzer(sdk.SeverityHigh))
	h.write("backend/build.sbt", "")
	if code := h.run("scan", "--fail-on", "high"); code != ExitRuntime || !h.exists("deepward-out/report.json") {
		t.Errorf("partial failure: exit %d", code)
	}
	if !strings.Contains(h.stdout.String(), "failed sbt:backend") || !strings.Contains(h.stdout.String(), "FAILED") {
		t.Errorf("summary:\n%s", h.stdout.String())
	}
	if code := h.run("scan", "--fail-on", "high", "--allow-partial"); code != ExitGate {
		t.Errorf("allow-partial: exit %d", code)
	}
	if code := h.run("scan", "--allow-partial", "--quiet"); code != ExitOK || h.stdout.Len() != 0 {
		t.Errorf("quiet: %d %q", code, h.stdout.String())
	}
}

func TestColorsAndLogs(t *testing.T) {
	h := newHarness(t, analyzer(sdk.SeverityCritical), &sdktest.FakeAnalyzer{ID: "lows", Func: func(context.Context, sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		return []sdk.Finding{sdktest.SimpleFinding("", "L", sdk.SeverityLow, ""), sdktest.SimpleFinding("", "M", sdk.SeverityMedium, ""),
			sdktest.SimpleFinding("", "N", sdk.SeverityNone, "")}, nil
	}})
	h.tty = true
	h.run("scan", "--fail-on", "low")
	if !strings.Contains(h.stdout.String(), "\x1b[") || !strings.Contains(h.stdout.String(), "informational") {
		t.Errorf("colors on a TTY:\n%q", h.stdout.String())
	}
	h.env["NO_COLOR"] = "1"
	h.run("scan")
	if strings.Contains(h.stdout.String(), "\x1b[") {
		t.Error("NO_COLOR")
	}
	delete(h.env, "NO_COLOR")
	h.run("scan", "--no-color", "--log-format", "json")
	if strings.Contains(h.stdout.String(), "\x1b[") {
		t.Error("--no-color")
	}
}

func TestCIDetection(t *testing.T) {
	gl := map[string]string{"GITLAB_CI": "true", "CI_PROJECT_URL": "https://git.acme.local/team/billing", "CI_COMMIT_SHA": "deadbeef",
		"CI_DEFAULT_BRANCH": "main", "CI_COMMIT_REF_NAME": "feature", "CI_API_V4_URL": "https://git.acme.local/api/v4", "CI_PROJECT_PATH": "team/billing"}
	c := detectCI(func(k string) string { return gl[k] })
	if c.Provider != "gitlab-ci" || c.RepoURL != gl["CI_PROJECT_URL"] || c.Commit != "deadbeef" || c.DefaultBranch != "main" || c.Branch != "feature" ||
		c.Platform != "gitlab" || c.Repo != "team/billing" {
		t.Errorf("gitlab: %+v", c)
	}
	ev := filepath.Join(t.TempDir(), "event.json")
	_ = os.WriteFile(ev, []byte(`{"repository":{"default_branch":"trunk"}}`), 0o600)
	gh := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/app", "GITHUB_SHA": "cafe", "GITHUB_REF_NAME": "1/merge",
		"GITHUB_HEAD_REF": "feature", "GITHUB_EVENT_PATH": ev}
	c = detectCI(func(k string) string { return gh[k] })
	if c.RepoURL != "https://github.com/acme/app" || c.DefaultBranch != "trunk" || c.Branch != "feature" || c.Platform != "github" {
		t.Errorf("github: %+v", c)
	}
	gh["GITHUB_EVENT_PATH"] = filepath.Join(t.TempDir(), "missing.json")
	if detectCI(func(k string) string { return gh[k] }).DefaultBranch != "" {
		t.Error("missing event file")
	}
	_ = os.WriteFile(ev, []byte(`not json`), 0o600)
	gh["GITHUB_EVENT_PATH"] = ev
	if detectCI(func(k string) string { return gh[k] }).DefaultBranch != "" {
		t.Error("bad event file")
	}
	if detectCI(func(k string) string { return map[string]string{"CI": "true"}[k] }).Provider != "generic" {
		t.Error("generic")
	}
	if detectCI(func(string) string { return "" }).Provider != "" {
		t.Error("none")
	}
	jenkins := CIContext{}.override("https://repo", "sha", "b", "main")
	if jenkins.RepoURL != "https://repo" || jenkins.Commit != "sha" || jenkins.Branch != "b" || jenkins.DefaultBranch != "main" {
		t.Errorf("override: %+v", jenkins)
	}

	h := newHarness(t)
	for k, v := range gl {
		h.env[k] = v
	}
	h.run("scan", "--format", "json")
	var res sdk.ScanResult
	data, _ := os.ReadFile(filepath.Join(h.dir, "deepward-out/report.json"))
	_ = json.Unmarshal(data, &res)
	if res.Run.RepoURL != gl["CI_PROJECT_URL"] || res.Run.Commit != "deadbeef" || res.Run.TargetBranch != "main" || res.Run.CI != "gitlab-ci" {
		t.Errorf("run metadata: %+v", res.Run)
	}
	h2 := newHarness(t)
	h2.run("scan", "--format", "json", "--repo-url", "https://jenkins/repo", "--commit", "1234", "--target-branch", "develop", "--branch", "x")
	data, _ = os.ReadFile(filepath.Join(h2.dir, "deepward-out/report.json"))
	_ = json.Unmarshal(data, &res)
	if res.Run.RepoURL != "https://jenkins/repo" || res.Run.Commit != "1234" || res.Run.TargetBranch != "develop" {
		t.Errorf("generic flags: %+v", res.Run)
	}
}

func TestReportFromJSON(t *testing.T) {
	h := newHarness(t, analyzer(sdk.SeverityHigh))
	h.run("scan", "--format", "json")
	if code := h.run("report", "--from", "deepward-out/report.json", "--format", "html", "--out", "rendered"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	b, _ := os.ReadFile(filepath.Join(h.dir, "rendered/report.html"))
	if string(b) != "html: 2 findings\n" {
		t.Errorf("rendered: %q", b)
	}
	if code := h.run("report", "--from", "deepward-out/report.json"); code != ExitOK || !h.exists("deepward-out/report.html") {
		t.Errorf("default html: %d", code)
	}
	h.write("bad.json", "{")
	h.write("old.json", `{"schemaVersion": 99}`)
	for _, args := range [][]string{{"report"}, {"report", "--from", "missing.json"}, {"report", "--from", "bad.json"}, {"report", "--from", "old.json"},
		{"report", "--from", "deepward-out/report.json", "--format", "pdf"}} {
		if code := h.run(args...); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestPluginsListAndVersion(t *testing.T) {
	h := newHarness(t, analyzer(sdk.SeverityHigh))
	h.write("deepward.yaml", "plugins:\n  vulns: {enabled: false}\n")
	if code := h.run("plugins", "list"); code != ExitOK {
		t.Fatal(h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "SDK "+sdk.SDKVersion) || !strings.Contains(out, "analyzer") || !strings.Contains(out, "vulns") || !strings.Contains(out, " no ") {
		t.Errorf("plugins list:\n%s", out)
	}
	h.run("plugins", "list", "--json")
	var payload struct {
		SDKVersion string                `json:"sdkVersion"`
		Plugins    []engine.PluginStatus `json:"plugins"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &payload); err != nil || len(payload.Plugins) != 5 {
		t.Errorf("json: %v %s", err, h.stdout.String())
	}
	h.run("version")
	if h.stdout.String() != "deepward 1.2.3 (commit abc, built 2026-11-26, sdk "+sdk.SDKVersion+")\n" {
		t.Errorf("version: %q", h.stdout.String())
	}
}

func TestDBUpdate(t *testing.T) {
	h := newHarness(t)
	if code := h.run("db", "update"); code != ExitUsage {
		t.Errorf("no updater: %d", code)
	}
	h2 := newHarness(t, &updater{FakeAnalyzer: sdktest.FakeAnalyzer{ID: "osv-offline"}})
	if code := h2.run("db", "update"); code != ExitOK || !strings.Contains(h2.stdout.String(), "osv-offline: downloaded 2 ecosystems") {
		t.Errorf("update: %d %s", code, h2.stdout.String())
	}
	h3 := newHarness(t, &updater{FakeAnalyzer: sdktest.FakeAnalyzer{ID: "osv-offline"}, err: errors.New("network down")})
	if code := h3.run("db", "update"); code != ExitRuntime || !strings.Contains(h3.stderr.String(), "network down") {
		t.Errorf("update error: %d %s", code, h3.stderr.String())
	}
}

func TestFixPublishGuard(t *testing.T) {
	h := newHarness(t)
	if code := h.run("fix", "--publish"); code != ExitUsage || !strings.Contains(h.stderr.String(), "DEEPWARD_GITLAB_TOKEN") {
		t.Errorf("publish without token: %d %s", code, h.stderr.String())
	}
}

func TestFlagsIntoPluginConfig(t *testing.T) {
	var cfg engine.Config
	r := &runFlags{sboms: []string{"services/api=build/bom.json", "./web=web.json"}, sarifs: []string{"semgrep=semgrep.sarif"}, offline: true, cacheDir: "c"}
	if err := r.apply(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Offline || cfg.Cache.Dir != "c" {
		t.Error("simple flags")
	}
	b, _ := yaml.Marshal(map[string]yaml.Node{"sbom": cfg.Plugins["sbom-import"], "sarif": cfg.Plugins["sarif-import"]})
	for _, want := range []string{"services/api: build/bom.json", "web: web.json", "semgrep: semgrep.sarif"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	setPluginEntry(&cfg, "sbom-import", "files", "web", "other.json")
	b, _ = yaml.Marshal(cfg.Plugins["sbom-import"])
	if !strings.Contains(string(b), "web: other.json") || strings.Contains(string(b), "web.json") {
		t.Errorf("replace entry:\n%s", b)
	}
	if err := (&runFlags{sboms: []string{"nope"}}).apply(&cfg); err == nil {
		t.Error("bad pair")
	}
	if err := (&runFlags{sarifs: []string{"=x"}}).apply(&cfg); err == nil {
		t.Error("bad sarif pair")
	}
	h := newHarness(t)
	if code := h.run("scan", "--sbom", "bad"); code != ExitUsage {
		t.Errorf("cli bad pair: %d", code)
	}
}

func TestHelpers(t *testing.T) {
	if ExpandEnvRefs("a ${env:X} b ${env:Y}", func(k string) string { return map[string]string{"X": "1"}[k] }) != "a 1 b " {
		t.Error("ExpandEnvRefs")
	}
	if envName("fail-on") != "DEEPWARD_FAIL_ON" {
		t.Error("envName")
	}
	if (&exitError{code: 1}).Error() != "exit 1" {
		t.Error("exitError")
	}
	if usage(nil) != nil || !isUsage(usagef("x")) || isUsage(errors.New("x")) {
		t.Error("usage helpers")
	}
	if got := splitList(" a, ,b "); len(got) != 2 {
		t.Errorf("splitList: %v", got)
	}
	env := ProcessEnv([]string{"version"})
	if env.Registry == nil || env.Getenv == nil {
		t.Error("ProcessEnv")
	}
	if Run(context.Background(), Env{Args: []string{"version"}}) != ExitOK {
		t.Error("Run with zero Env")
	}
	if (Env{}).getenv("X") != "" {
		t.Error("nil getenv")
	}
}

func containsPlugin(ps []sdk.Plugin, p sdk.Plugin) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

// builtinReporter is a plugin that reports sdk.BuiltinVersion.
type builtinReporter struct{ *sdktest.FakeReporter }

func (b builtinReporter) Info() sdk.Info {
	i := b.FakeReporter.Info()
	i.Version = sdk.BuiltinVersion
	return i
}

func TestBuiltinVersionAndVersionFlag(t *testing.T) {
	h := newHarness(t, builtinReporter{&sdktest.FakeReporter{ID: "pdf", Name: "pdf"}})
	h.run("plugins", "list", "--json")
	var payload struct {
		Plugins []engine.PluginStatus `json:"plugins"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range payload.Plugins {
		if p.Info.ID == "pdf" {
			found = p.Info.Version == "1.2.3"
		}
	}
	if !found {
		t.Errorf("built-in plugins show the Deepward version: %s", h.stdout.String())
	}
	if code := h.run("--version"); code != ExitOK || h.stdout.String() != "deepward 1.2.3 (commit abc, built 2026-11-26, sdk "+sdk.SDKVersion+")\n" {
		t.Errorf("--version: %d %q", code, h.stdout.String())
	}
}
