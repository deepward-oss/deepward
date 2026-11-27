//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/internal/cli"
	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// countingTools counts the dependency tree runs of mvn.
type countingTools struct {
	sdk.Tools
	trees *atomic.Int64
}

func (c countingTools) Require(ctx context.Context, req sdk.ToolRequirement, p *sdk.Project) (sdk.Tool, error) {
	t, err := c.Tools.Require(ctx, req, p)
	if err != nil || req.Name != "mvn" {
		return t, err
	}
	return countingTool{Tool: t, trees: c.trees}, nil
}

type countingTool struct {
	sdk.Tool
	trees *atomic.Int64
}

func (c countingTool) Run(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
	for _, a := range spec.Args {
		if strings.HasPrefix(a, "-DoutputFile=") {
			c.trees.Add(1)
		}
	}
	return c.Tool.Run(ctx, spec)
}

// diffLines keeps the lines of the dry-run output that describe change
// requests and their edits, dropping timings.
func diffLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") || strings.HasPrefix(l, "@@") || strings.Contains(l, "Deepward:") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// TestBatchVerificationRealMaven fixes the maven-many fixture with real
// Maven, verifying every change on its own and in batches, and compares the
// change requests, the mvn runs and the time.
func TestBatchVerificationRealMaven(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available")
	}
	cacheRoot := os.Getenv("DEEPWARD_IT_CACHE")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "deepward-it")
	}
	ws := filepath.Join(cacheRoot, "maven-many")
	home := filepath.Join(ws, ".home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0o755)
	entries, _ := os.ReadDir(ws)
	for _, e := range entries {
		if e.Name() != ".home" {
			_ = os.RemoveAll(filepath.Join(ws, e.Name()))
		}
	}
	copyDir(t, filepath.Join(repoRoot(t), "testdata", "projects", "maven-many"), ws)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	var trees atomic.Int64
	newTools := func(ctx context.Context, cfg engine.Config, w *sdk.Workspace, reg *sdk.Registry, cacheDir string, env cli.Env) (sdk.Tools, error) {
		t, err := cli.DefaultTools(ctx, cfg, w, reg, cacheDir, env)
		if err != nil {
			return nil, err
		}
		return countingTools{Tools: t, trees: &trees}, nil
	}
	type outcome struct {
		code     int
		trees    int64
		duration time.Duration
		out, err string
	}
	run := func(mode string, args ...string) outcome {
		cfg := "output:\n  formats: [json]\nexclude: [.home]\nplugins:\n  outdated-dependencies:\n    enabled: false\n" +
			"fix:\n  verify: " + mode + "\n" +
			"tools:\n  mvn:\n    resolvers: [container]\n    image: maven:3.9.9-eclipse-temurin-17\n    timeout: 20m\n" +
			"    args: [\"-Dmaven.repo.local=" + home + "/.m2/repository\"]\n" +
			"    env:\n      HOME: " + home + "\n      JAVA_TOOL_OPTIONS: -Duser.home=" + home + "\n      TMPDIR: " + home + "/tmp\n"
		if err := os.WriteFile(filepath.Join(ws, "deepward.yaml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		trees.Store(0)
		var out, errb bytes.Buffer
		start := time.Now()
		code := cli.Run(ctx, cli.Env{Args: append(args, "--cache-dir", filepath.Join(cacheRoot, "cache")), Stdout: &out, Stderr: &errb,
			Cwd: ws, Registry: sdk.DefaultRegistry(), Getenv: os.Getenv, Environ: os.Environ, NewTools: newTools})
		return outcome{code: code, trees: trees.Load(), duration: time.Since(start), out: out.String(), err: errb.String()}
	}
	// The warm-up scan downloads the dependencies, so neither mode pays for it.
	if warm := run(engine.VerifyEach, "scan"); warm.code != cli.ExitGate && warm.code != 0 {
		t.Fatalf("warm-up scan exit %d\n%s\n%s", warm.code, warm.out, warm.err)
	}
	each := run(engine.VerifyEach, "fix")
	batch := run(engine.VerifyBatch, "fix")
	table := fmt.Sprintf("mode | mvn runs | duration\neach | %d | %s\nbatch | %d | %s\n",
		each.trees, each.duration.Round(time.Second), batch.trees, batch.duration.Round(time.Second))
	fmt.Print(table)
	t.Log("\n" + table)
	for name, o := range map[string]outcome{"each": each, "batch": batch} {
		if o.code != 0 || !strings.Contains(o.out, "+++ b/") {
			t.Fatalf("%s: fix exit %d without changes\n%s\n%s", name, o.code, o.out, o.err)
		}
	}
	if a, b := diffLines(each.out), diffLines(batch.out); a != b {
		t.Errorf("the modes produce different change requests:\n--- each ---\n%s\n--- batch ---\n%s", a, b)
	}
	if each.trees < 3*batch.trees {
		t.Errorf("batch must need at least three times fewer mvn runs: each %d, batch %d", each.trees, batch.trees)
	}
}
