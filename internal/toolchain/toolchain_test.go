package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// TestHelperProcess is not a real test: it is the fake tl executed by the
// tests below (re-exec of the test binary).
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
	switch args[0] {
	case "version":
		fmt.Printf("Apache Maven %s\nJava version: 17\n", args[1])
	case "env":
		env := os.Environ()
		sort.Strings(env)
		for _, kv := range env {
			fmt.Println(kv)
		}
	case "args":
		wd, _ := os.Getwd()
		fmt.Println("dir=" + wd)
		fmt.Println(strings.Join(args[1:], " "))
	case "lines":
		n, _ := strconv.Atoi(args[1])
		for i := 1; i <= n; i++ {
			fmt.Fprintf(os.Stderr, "line %d secret-value-42\n", i)
		}
		fmt.Fprint(os.Stderr, "partial")
		os.Exit(3)
	case "sleep":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func helper(args ...string) []string {
	return append([]string{os.Args[0], "-test.run=TestHelperProcess", "--"}, args...)
}

var helperEnv = map[string]string{"GO_WANT_HELPER": "1"}

// scripted is a resolver returning a fixed candidate and counting calls.
type scripted struct {
	id    string
	cmd   []string
	err   error
	calls atomic.Int64
}

func (s *scripted) Info() sdk.Info {
	return sdk.Info{ID: s.id, Kind: sdk.KindToolResolver, Version: "1", Description: "scripted"}
}

func (s *scripted) Resolve(context.Context, sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	s.calls.Add(1)
	if s.err != nil || s.cmd == nil {
		return nil, s.err
	}
	return &sdk.ToolCandidate{Command: s.cmd, Env: helperEnv, Source: s.id + "-source"}, nil
}

func newChain(t *testing.T, settings map[string]sdk.ToolSettings, resolvers ...sdk.ToolResolver) *Toolchain {
	t.Helper()
	tc, err := New(Options{Resolvers: resolvers, Settings: settings, Workspace: &sdk.Workspace{Root: t.TempDir()},
		Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=/home/ci", "FOO=not-allowed", "DEEPWARD_GITLAB_TOKEN=glpat-123456", "MAVEN_OPTS=-Xmx1g"},
		Getenv:  func(k string) string { return map[string]string{"NEXUS_TOKEN": "nexus-secret-1"}[k] }, Secrets: []string{"glpat-123456"}})
	if err != nil {
		t.Fatal(err)
	}
	return tc
}

func TestResolveProbesVersionAndChecksConstraint(t *testing.T) {
	wrapper := &scripted{id: "wrapper", cmd: helper("version", "1.3.13")}
	path := &scripted{id: "path", cmd: helper("version", "1.10.2")}
	tc := newChain(t, nil, &scripted{id: "config-path"}, wrapper, path)
	project := &sdk.Project{ID: "sbt:.", Dir: "."}
	tl, err := tc.Require(context.Background(), sdk.ToolRequirement{Name: "mvn", Constraint: ">=1.4"}, project)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Version() != "1.10.2" || tl.Resolver() != "path" || tl.Name() != "mvn" || len(tl.Command()) == 0 {
		t.Errorf("tl: %s %s", tl.Version(), tl.Resolver())
	}
	d := tc.Diagnose(context.Background(), sdk.ToolRequirement{Name: "mvn", Constraint: ">=1.4"}, project)
	if len(d.Attempts) != 3 || !strings.Contains(d.Attempts[1].Outcome, "mvn 1.3.13 does not satisfy >=1.4") || d.Attempts[0].Outcome != "not found" {
		t.Errorf("attempts: %+v", d.Attempts)
	}
	if wrapper.calls.Load() != 1 {
		t.Error("diagnosis must be cached")
	}
	only := newChain(t, nil, &scripted{id: "config-path"}, &scripted{id: "wrapper", cmd: helper("version", "1.3.13")}, &scripted{id: "path"})
	_, err = only.Require(context.Background(), sdk.ToolRequirement{Name: "mvn", Constraint: ">=1.4"}, project)
	if !errors.Is(err, sdk.ErrToolVersionMismatch) || err.Error() != "tool-version-mismatch: mvn 1.3.13 does not satisfy >=1.4" {
		t.Errorf("mismatch: %q is=%v", err.Error(), errors.Is(err, sdk.ErrToolVersionMismatch))
	}
	none := newChain(t, nil, &scripted{id: "config-path"}, &scripted{id: "wrapper", err: errors.New("boom")})
	_, err = none.Require(context.Background(), sdk.ToolRequirement{Name: "sbt"}, nil)
	if !errors.Is(err, sdk.ErrToolNotFound) || !strings.Contains(err.Error(), "tried: config-path, wrapper, path") {
		t.Errorf("not found: %v", err)
	}
	if _, err := none.Require(context.Background(), sdk.ToolRequirement{Name: "sbt", Constraint: "~~1"}, nil); !errors.Is(err, sdk.ErrToolVersionMismatch) {
		t.Errorf("bad constraint: %v", err)
	}
}

func TestProbeFailures(t *testing.T) {
	tc := newChain(t, map[string]sdk.ToolSettings{
		"custom": {VersionArgs: []string{"args"}, VersionPattern: `NOPE (\S+)`},
		"broken": {Resolvers: []string{"path"}},
	}, &scripted{id: "config-path"}, &scripted{id: "wrapper"}, &scripted{id: "path", cmd: helper()})
	d := tc.Diagnose(context.Background(), sdk.ToolRequirement{Name: "custom"}, nil)
	if d.OK || !strings.Contains(d.Attempts[2].Outcome, "version not found") {
		t.Errorf("custom: %+v", d.Attempts)
	}
	missing := newChain(t, nil, &scripted{id: "path", cmd: []string{"/definitely/missing/tl"}})
	d = missing.Diagnose(context.Background(), sdk.ToolRequirement{Name: "mvn"}, nil)
	if d.OK || !strings.Contains(d.Attempts[len(d.Attempts)-1].Outcome, "version probe failed") {
		t.Errorf("missing binary: %+v", d.Attempts)
	}
	// A tl without probe definition resolves with an unknown version.
	plain := newChain(t, nil, &scripted{id: "path", cmd: helper()})
	tl, err := plain.Require(context.Background(), sdk.ToolRequirement{Name: "semgrep"}, nil)
	if err != nil || tl.Version() != "" {
		t.Errorf("plain tl: %v", err)
	}
	if _, err := plain.Require(context.Background(), sdk.ToolRequirement{Name: "semgrep", Constraint: ">=1"}, nil); !errors.Is(err, sdk.ErrToolVersionMismatch) {
		t.Errorf("unknown version with constraint: %v", err)
	}
	// The probe tolerates a non-zero exit when the version is printed.
	exitTC := newChain(t, map[string]sdk.ToolSettings{"x": {VersionArgs: []string{"lines", "1"}, VersionPattern: `line (\d+)`}}, &scripted{id: "path", cmd: helper()})
	if d := exitTC.Diagnose(context.Background(), sdk.ToolRequirement{Name: "x"}, nil); !d.OK || d.Version != "1" {
		t.Errorf("non-zero probe: %+v", d)
	}
	exitTC2 := newChain(t, map[string]sdk.ToolSettings{"y": {VersionArgs: []string{"lines", "1"}, VersionPattern: `NOPE (\d+)`}}, &scripted{id: "path", cmd: helper()})
	if d := exitTC2.Diagnose(context.Background(), sdk.ToolRequirement{Name: "y"}, nil); d.OK {
		t.Error("non-zero probe without version must fail")
	}
}

func TestRunAppliesSettingsEnvAndMasking(t *testing.T) {
	jh := t.TempDir()
	settings := map[string]sdk.ToolSettings{"mvn": {
		Args: []string{"-s", "ci/settings.xml"}, Env: map[string]string{"NEXUS_TOKEN": "${env:NEXUS_TOKEN}", "MODE": "ci"},
		JavaHome: jh, JVMOpts: "-Xmx2g", Workdir: "sub",
	}}
	path := &scripted{id: "path", cmd: helper("version", "3.9.9")}
	tc := newChain(t, settings, &scripted{id: "config-path"}, &scripted{id: "wrapper"}, path)
	_ = os.MkdirAll(filepath.Join(tc.opts.Workspace.Root, "sub"), 0o755)
	tl, err := tc.Require(context.Background(), sdk.ToolRequirement{Name: "mvn"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.(*tool).cand = &sdk.ToolCandidate{Command: helper("env"), Env: helperEnv}
	res, err := tl.Run(context.Background(), sdk.RunSpec{Env: map[string]string{"EXTRA": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Stdout)
	for _, want := range []string{"MAVEN_OPTS=-Xmx2g", "JAVA_HOME=" + jh, "PATH=" + filepath.Join(jh, "bin"), "MODE=ci", "EXTRA=1", "NEXUS_TOKEN=***", "HOME=/home/ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("env lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"FOO=", "DEEPWARD_GITLAB_TOKEN", "nexus-secret-1"} {
		if strings.Contains(out, bad) {
			t.Errorf("env must not contain %q", bad)
		}
	}
	// Args and working directory.
	tool2 := tl.(*tool)
	tool2.cand = &sdk.ToolCandidate{Command: helper("args"), Env: helperEnv}
	res, err = tool2.Run(context.Background(), sdk.RunSpec{Args: []string{"dependency:tree"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Stdout), "-s ci/settings.xml dependency:tree") || !strings.Contains(string(res.Stdout), "dir="+filepath.Join(tc.opts.Workspace.Root, "sub")) {
		t.Errorf("args/dir:\n%s", res.Stdout)
	}
}

func TestRunTailExitAndTimeout(t *testing.T) {
	tc := newChain(t, map[string]sdk.ToolSettings{"t": {Timeout: sdk.Duration(200 * time.Millisecond)}}, &scripted{id: "path", cmd: helper()})
	tl, err := tc.Require(context.Background(), sdk.ToolRequirement{Name: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	x := tl.(*tool)
	x.cand = &sdk.ToolCandidate{Command: helper("lines", "250"), Env: helperEnv}
	res, err := x.Run(context.Background(), sdk.RunSpec{Secrets: []string{"secret-value-42"}, Timeout: 5 * time.Second})
	var xe *sdk.ExitError
	if !errors.As(err, &xe) || xe.ExitCode != 3 || res.ExitCode != 3 {
		t.Fatalf("exit: %v %+v", err, res.ExitCode)
	}
	if len(res.Tail) != TailLines || !strings.HasPrefix(res.Tail[len(res.Tail)-1], "partial") || res.Tail[0] != "line 52 ***" {
		t.Errorf("tail: %d first=%q last=%q", len(res.Tail), res.Tail[0], res.Tail[len(res.Tail)-1])
	}
	x.cand = &sdk.ToolCandidate{Command: helper("sleep"), Env: helperEnv}
	start := time.Now()
	_, err = x.Run(context.Background(), sdk.RunSpec{})
	if !errors.As(err, &xe) || xe.ExitCode != -1 || !strings.Contains(strings.Join(xe.Tail, "\n"), "timed out") || time.Since(start) > 8*time.Second {
		t.Errorf("timeout: %v", err)
	}
	if _, err := execute(context.Background(), process{}); err == nil {
		t.Error("empty command")
	}
	if _, err := execute(context.Background(), process{argv: []string{"/missing/binary"}}); err == nil {
		t.Error("missing binary")
	}
}

func TestPlaceholders(t *testing.T) {
	tc := newChain(t, map[string]sdk.ToolSettings{"sbt": {Env: map[string]string{"B": "2"}, JVMOpts: "-Xss4m"}}, &scripted{id: "path", cmd: helper("version", "1.10.0")})
	tl, _ := tc.Require(context.Background(), sdk.ToolRequirement{Name: "sbt"}, nil)
	x := tl.(*tool)
	x.cand = &sdk.ToolCandidate{Command: append(helper("args", "{{dirmount}}", "-w", "{{dir}}", "{{env}}"), "image"), Env: helperEnv,
		MapPath: func(p string) string { return "/mapped" + p }}
	res, err := x.Run(context.Background(), sdk.RunSpec{Dir: tc.opts.Workspace.Root, Env: map[string]string{"A": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "-w /mapped" + tc.opts.Workspace.Root + " -e A -e B -e GO_WANT_HELPER -e SBT_OPTS image"
	if !strings.Contains(string(res.Stdout), want) || strings.Contains(string(res.Stdout), "-v ") {
		t.Errorf("placeholders:\n%s\nwant %s", res.Stdout, want)
	}
	// A directory outside the workspace (a fix scratch copy) is mounted too.
	outside := t.TempDir()
	res, err = x.Run(context.Background(), sdk.RunSpec{Dir: outside})
	if err != nil {
		t.Fatal(err)
	}
	if want := "-v /mapped" + outside + ":/mapped" + outside + " -w /mapped" + outside; !strings.Contains(string(res.Stdout), want) {
		t.Errorf("outside mount:\n%s\nwant %s", res.Stdout, want)
	}
}

func TestNewValidation(t *testing.T) {
	_, err := New(Options{Resolvers: []sdk.ToolResolver{&scripted{id: "path"}}, Settings: map[string]sdk.ToolSettings{
		"a": {Resolvers: []string{"nope"}},
		"b": {Resolvers: []string{"container"}},
		"c": {Download: &sdk.DownloadSettings{URL: "https://x/semgrep.zip"}},
		"d": {Resolvers: []string{"download"}},
		"e": {VersionPattern: "("},
	}})
	for _, want := range []string{`tools.a.resolvers: unknown resolver "nope"`, "tools.b.image is required", "tools.c.download.sha256 is required",
		"tools.d.download.url is required", "tools.e.versionPattern"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	tc, err := New(Options{Settings: map[string]sdk.ToolSettings{
		"semgrep": {Image: "returntocorp/semgrep", Download: &sdk.DownloadSettings{URL: "u", SHA256: "abc"}},
	}, Definitions: []sdk.ToolDefinition{{Name: "sbt", Wrappers: []string{"./sbtx"}, JVMOptsEnv: "X", Executables: []string{"sbt"}, VersionArgs: []string{"v"}, VersionPattern: "p"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tc.order(tc.opts.Settings["semgrep"]), ","); got != "config-path,wrapper,path,container,download" {
		t.Errorf("implicit order: %s", got)
	}
	if d := tc.definition("sbt"); d.Wrappers[0] != "./sbtx" || d.JVMOptsEnv != "X" {
		t.Errorf("merged definition: %+v", d)
	}
	if d := tc.definition("unknown"); d.Executables[0] != "unknown" {
		t.Error("default definition")
	}
	d := tc.Diagnose(context.Background(), sdk.ToolRequirement{Name: "mvn"}, nil)
	if d.OK || d.Attempts[0].Outcome != "resolver not available" {
		t.Errorf("no resolvers: %+v", d)
	}
}

func TestConstraint(t *testing.T) {
	cases := []struct {
		c, v string
		ok   bool
	}{
		{"", "1", true}, {">=1.4", "1.10.2", true}, {">=1.4", "1.3.13", false}, {">=3.6, <4", "3.9.9", true}, {">=3.6, <4", "4.0.0", false},
		{"=8.0.100", "8.0.100", true}, {"==8.0", "8.0.0", true}, {"!=1.0", "1.0", false}, {">1.0", "1.0.1", true}, {"<=2", "2.0.0", true},
		{"<=2", "2.0.1", false}, {">=1.0", "1.0.0-RC1", false}, {">=1.0-RC1", "1.0-RC2", true}, {">=1.0-RC2", "1.0-RC1", false},
		{">=1", "garbage", false}, {"1.2", "v1.2", true},
	}
	for _, c := range cases {
		con, err := ParseConstraint(c.c)
		if err != nil {
			t.Fatalf("%s: %v", c.c, err)
		}
		if got := con.Allows(c.v); got != c.ok {
			t.Errorf("%q allows %q = %v, want %v", c.c, c.v, got, c.ok)
		}
	}
	if _, err := ParseConstraint(">= x"); err == nil {
		t.Error("invalid constraint")
	}
	if c, _ := ParseConstraint(" >=1 "); c.String() != ">=1" {
		t.Error("String")
	}
}

func TestEnvHelpers(t *testing.T) {
	env := filterEnv([]string{"PATH=/bin", "LC_ALL=C", "MAVEN_OPTS=x", "FOO=y", "DEEPWARD_GITHUB_TOKEN=t", "DEEPWARD_CONFIG=c", "broken", "=x"})
	if len(env) != 3 || env["LC_ALL"] != "C" {
		t.Errorf("filterEnv: %v", env)
	}
	m := newMasker([]string{"abc", "longsecret", "longsecretvalue"})
	if string(m.mask([]byte("x longsecretvalue abc"))) != "x *** abc" {
		t.Error("masker: longest first, short values ignored")
	}
}
