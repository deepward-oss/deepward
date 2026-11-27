package engine

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// graphWorld is a fake ecosystem whose dependency graph is computed from
// <project>/versions.txt ("name=version" lines). It counts graph builds,
// fails the build when breaks says so and reports a vulnerability for every
// package below 2.0 (or as vulnerable says), fixed by the alternatives in alts.
type graphWorld struct {
	alts       map[string][]sdk.Remediation
	breaks     func(v map[string]string) bool
	vulnerable func(name string, v map[string]string) bool
	builds     atomic.Int64
	// manual lists packages the ecosystem cannot edit.
	manual map[string]bool
}

var worldLine = regexp.MustCompile(`(?m)^(\w+)=([0-9.]+)$`)

func worldVersions(fsys fs.FS, dir string) map[string]string {
	data, _ := fs.ReadFile(fsys, path.Join(dir, "versions.txt"))
	out := map[string]string{}
	for _, m := range worldLine.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2]
	}
	return out
}

// bump is a remediation that sets name to "to" and affects affects (the
// package itself when nil; none when empty).
func bump(name, to string, affects ...string) sdk.Remediation {
	if affects == nil {
		affects = []string{"pkg:maven/g/" + name}
	}
	return sdk.Remediation{Description: name + " → " + to, Group: "security/g:" + name, Affects: affects,
		Package: &sdk.PackageChange{PURL: "pkg:maven/g/" + name, To: to}}
}

func (w *graphWorld) engine(t *testing.T, ws *sdk.Workspace, cfg string) *Engine {
	t.Helper()
	eco := &sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml", Facts: map[string]func(context.Context, sdk.FactRequest) (any, error){
		"graph": func(_ context.Context, req sdk.FactRequest) (any, error) {
			w.builds.Add(1)
			if w.breaks != nil && w.breaks(worldVersions(req.Workspace.FS, req.Project.Dir)) {
				return nil, &sdk.ExitError{Tool: "mvn", ExitCode: 1}
			}
			return "ok", nil
		}},
		RealizeFunc: func(_ context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
			file := path.Join(req.Project.Dir, "versions.txt")
			data, err := fs.ReadFile(req.Workspace.FS, file)
			if err != nil {
				return sdk.RealizeResult{}, err
			}
			var res sdk.RealizeResult
			for _, c := range req.Changes {
				name := c.PURL[strings.LastIndex(c.PURL, "/")+1:]
				if w.manual[name] {
					res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "managed elsewhere"})
					continue
				}
				if loc := regexp.MustCompile(`(?m)^` + name + `=([0-9.]+)$`).FindSubmatchIndex(data); loc != nil {
					res.Edits = append(res.Edits, sdk.TextEdit{Path: file, Start: loc[2], End: loc[3], Replacement: c.To})
				}
			}
			return res, nil
		}}
	analyzer := &sdktest.FakeAnalyzer{ID: "vulns", Needs: []string{"graph"}, Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		v := worldVersions(req.Workspace.FS, req.Project.Dir)
		var out []sdk.Finding
		for name, alts := range w.alts {
			bad := v[name] != "" && v[name] < "2.0"
			if w.vulnerable != nil {
				bad = w.vulnerable(name, v)
			}
			if !bad {
				continue
			}
			out = append(out, sdk.Finding{Rule: "CVE-" + name, Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh, Message: "vulnerable " + name,
				Locations:    []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/g/" + name + "@" + v[name], Direct: true}}},
				Remediations: alts})
		}
		return out, nil
	}}
	reg := sdk.NewRegistry()
	_ = reg.Register(eco)
	_ = reg.Register(analyzer)
	e, err := New(context.Background(), Options{Registry: reg, Config: parseConfig(t, cfg), Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// worldWorkspace makes one project per directory with the given versions.
func worldWorkspace(t *testing.T, projects map[string]string) *sdk.Workspace {
	t.Helper()
	root := t.TempDir()
	for dir, content := range projects {
		_ = os.MkdirAll(filepath.Join(root, dir), 0o755)
		_ = os.WriteFile(filepath.Join(root, dir, "pom.xml"), []byte("<project/>"), 0o644)
		_ = os.WriteFile(filepath.Join(root, dir, "versions.txt"), []byte(content), 0o644)
	}
	return sdk.NewWorkspace(root, nil)
}

// planSummary renders everything a plan publishes, in order.
func planSummary(plan *FixPlan) string {
	var sb strings.Builder
	for _, cs := range plan.ChangeSets {
		sb.WriteString(cs.Branch + "\n" + cs.Diff)
		for _, it := range cs.Items {
			fmt.Fprintf(&sb, "item %s %s unverified=%v %s\n", it.Package, it.To, it.Unverified, it.VerifyFailure)
		}
	}
	for _, m := range plan.Manual {
		sb.WriteString("manual " + m.Group + " " + m.Reason + "\n")
	}
	for _, f := range plan.Failed {
		sb.WriteString("failed " + f.Group + " " + f.Reason + "\n")
	}
	return sb.String()
}

// runModes fixes the same workspace with both modes and returns the
// summaries and graph builds; it checks GraphBuilds against the builds the
// fake ecosystem saw (minus the scan before verification).
func runModes(t *testing.T, w *graphWorld, projects map[string]string, cfg string) (batch, each string, batchBuilds, eachBuilds int) {
	t.Helper()
	run := func(mode string) (string, int) {
		w.builds.Store(0)
		plan, err := w.engine(t, worldWorkspace(t, projects), "fix:\n  verify: "+mode+"\n"+cfg).PlanFixes(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := int(w.builds.Load()) - len(projects); got != plan.GraphBuilds {
			t.Errorf("%s: GraphBuilds %d, the ecosystem built %d graphs after the scan", mode, plan.GraphBuilds, got)
		}
		return planSummary(plan), plan.GraphBuilds
	}
	batch, batchBuilds = run(VerifyBatch)
	each, eachBuilds = run(VerifyEach)
	if batch != each {
		t.Errorf("modes disagree:\n--- batch ---\n%s\n--- each ---\n%s", batch, each)
	}
	return batch, each, batchBuilds, eachBuilds
}

func packages(n int, version string) (string, []string) {
	var sb strings.Builder
	var names []string
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("p%02d", i)
		names = append(names, name)
		fmt.Fprintf(&sb, "%s=%s\n", name, version)
	}
	return sb.String(), names
}

func TestBatchIndependentGroups(t *testing.T) {
	content, names := packages(12, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
	}
	summary, _, batch, each := runModes(t, w, map[string]string{"app": content}, "")
	if batch != 1 || each != 12 {
		t.Errorf("graph builds: batch %d (want 1), each %d (want 12)", batch, each)
	}
	if strings.Count(summary, "item ") != 12 || strings.Contains(summary, "failed") {
		t.Errorf("twelve change sets:\n%s", summary)
	}
}

func TestBatchSecondAlternatives(t *testing.T) {
	content, names := packages(12, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}}
	for i, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
		if i%2 == 0 {
			// 1.5 builds but is still vulnerable: the second alternative is needed.
			w.alts[n] = []sdk.Remediation{bump(n, "1.5"), bump(n, "2.0")}
		}
	}
	summary, _, batch, each := runModes(t, w, map[string]string{"app": content}, "")
	if batch != 2 || each != 18 {
		t.Errorf("graph builds: batch %d (want 2, one per round), each %d (want 18)", batch, each)
	}
	if strings.Count(summary, " 2.0 unverified=false") != 12 {
		t.Errorf("every package at 2.0:\n%s", summary)
	}
	// With fix.unverified.publish the rejected first alternatives stay
	// rejected because the second ones pass.
	unverified, _, _, _ := runModes(t, w, map[string]string{"app": content}, "  unverified:\n    publish: true\n")
	if unverified != summary {
		t.Errorf("unverified.publish changes nothing when a later alternative passes:\n%s", unverified)
	}
}

func TestBatchBisection(t *testing.T) {
	content, names := packages(8, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}, breaks: func(v map[string]string) bool { return v["p05"] == "2.0" }}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
	}
	summary, _, batch, each := runModes(t, w, map[string]string{"app": content}, "")
	if batch > 1+2*3 || each != 8 {
		t.Errorf("graph builds: batch %d (want at most 7), each %d (want 8)", batch, each)
	}
	if strings.Count(summary, "item ") != 7 || !strings.Contains(summary, "failed security/g:p05 verification-failed: the project no longer builds") {
		t.Errorf("p05 rejected, the seven others accepted:\n%s", summary)
	}
}

func TestBatchPairBreakingTogether(t *testing.T) {
	content, names := packages(4, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}, breaks: func(v map[string]string) bool { return v["p01"] == "2.0" && v["p02"] == "2.0" }}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
	}
	// Each change builds on its own, so fix.verify: each accepts all four;
	// the batch falls back to one by one and agrees.
	summary, _, _, _ := runModes(t, w, map[string]string{"app": content}, "")
	if strings.Count(summary, "item ") != 4 {
		t.Errorf("all four accepted:\n%s", summary)
	}
	// A unit that breaks the build with an accepted change of its own group
	// is rejected in both modes.
	w.alts = map[string][]sdk.Remediation{"p01": {bump("p01", "2.0")}, "p02": {bump("p02", "2.0")}}
	grouped, _, _, _ := runModes(t, w, map[string]string{"app": content}, "  groups:\n    - name: pair\n      match: [\"pkg:maven/g/*\"]\n")
	if strings.Count(grouped, "item ") != 1 || !strings.Contains(grouped, "no longer builds") {
		t.Errorf("one of the pair accepted:\n%s", grouped)
	}
}

func TestBatchUpgradeAndPinOfTheSamePackage(t *testing.T) {
	// Upgrading parent to 2.0 brings a fixed child; the pin of child is the
	// other group's fix. They affect the same package, so they never share
	// a batch and the upgrade is accepted only on its own merits.
	w := &graphWorld{
		alts: map[string][]sdk.Remediation{
			"parent": {bump("parent", "2.0", "pkg:maven/g/parent", "pkg:maven/g/child")},
			"child":  {bump("child", "2.0", "pkg:maven/g/child")},
		},
		vulnerable: func(name string, v map[string]string) bool {
			if name == "child" {
				return v["child"] < "2.0" && v["parent"] < "2.0"
			}
			return v[name] < "2.0"
		},
	}
	summary, _, batch, each := runModes(t, w, map[string]string{"app": "parent=1.0\nchild=1.0\n"}, "")
	if batch != 2 || each != 2 {
		t.Errorf("graph builds: batch %d (want 2, separate batches), each %d", batch, each)
	}
	if strings.Count(summary, "item ") != 2 {
		t.Errorf("both accepted:\n%s", summary)
	}
}

func TestBatchChangesWithoutAffects(t *testing.T) {
	content, names := packages(3, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
	}
	w.alts["p01"] = []sdk.Remediation{bump("p01", "2.0", []string{}...)}
	_, _, batch, _ := runModes(t, w, map[string]string{"app": content}, "")
	if batch != 2 {
		t.Errorf("a change without affected packages gets a batch of its own: %d builds", batch)
	}
}

func TestBatchProjectsAndConcurrency(t *testing.T) {
	content, names := packages(6, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}, breaks: func(v map[string]string) bool { return v["p03"] == "2.0" }}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "1.5"), bump(n, "2.0")}
	}
	projects := map[string]string{"api": content, "web": content}
	one, _, batch, each := runModes(t, w, projects, "  concurrency: 1\n")
	many, _, _, _ := runModes(t, w, projects, "  concurrency: 8\n")
	if one != many {
		t.Errorf("results depend on concurrency:\n--- 1 ---\n%s\n--- 8 ---\n%s", one, many)
	}
	if batch >= each {
		t.Errorf("batches never mix projects but still save rebuilds: batch %d, each %d", batch, each)
	}
	if !strings.Contains(one, "deepward/maven/api/security-p01") || !strings.Contains(one, "deepward/maven/web/security-p01") ||
		strings.Count(one, "no longer builds") != 2 {
		t.Errorf("both projects fixed, p03 rejected in each:\n%s", one)
	}
}

// cancelOn cancels a context when a log record with the message is handled.
type cancelOn struct {
	slog.Handler
	msg    string
	cancel context.CancelFunc
}

func (c cancelOn) Enabled(context.Context, slog.Level) bool { return true }

func (c cancelOn) Handle(_ context.Context, r slog.Record) error {
	if r.Message == c.msg {
		c.cancel()
	}
	return nil
}

func TestBatchCancelled(t *testing.T) {
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		w := &graphWorld{alts: map[string][]sdk.Remediation{"p01": {bump("p01", "2.0")}}}
		e := w.engine(t, worldWorkspace(t, map[string]string{"app": "p01=1.0\n"}), "fix:\n  verify: "+mode+"\n")
		ctx, cancel := context.WithCancel(context.Background())
		// Cancelled after the last plugin call of the run.
		e.log = slog.New(cancelOn{Handler: slog.DiscardHandler, msg: "fix group done", cancel: cancel})
		if _, err := e.PlanFixes(ctx); err == nil {
			t.Errorf("%s: a cancelled run returns no plan", mode)
		}
	}
}

func TestBatchManualChanges(t *testing.T) {
	content, names := packages(3, "1.0")
	w := &graphWorld{alts: map[string][]sdk.Remediation{}, manual: map[string]bool{"p02": true}}
	for _, n := range names {
		w.alts[n] = []sdk.Remediation{bump(n, "2.0")}
	}
	summary, _, batch, each := runModes(t, w, map[string]string{"app": content}, "")
	if batch != 1 || each != 2 || !strings.Contains(summary, "manual security/g:p02 managed elsewhere") || strings.Count(summary, "item ") != 2 {
		t.Errorf("a change the ecosystem cannot make leaves the batch before the rebuild (batch %d, each %d):\n%s", batch, each, summary)
	}
}
