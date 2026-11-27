package engine

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func branches(plan *FixPlan) string {
	var out []string
	for _, cs := range plan.ChangeSets {
		out = append(out, cs.Branch)
	}
	return strings.Join(out, ",")
}

func TestFixMinSeverity(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\nother=1.0\nboth=1.0\n")
	e := fixEngine(t, ws, "fix:\n  minSeverity: critical\n", true)
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only CVE-2 is critical; CVE-1 (high, same package), CVE-both (medium)
	// and the updates of other and both (no severity) are left out.
	if got := branches(plan); got != "deepward/maven/app/security-lib" {
		t.Fatalf("change sets: %s", got)
	}
	if plan.Filtered != 4 || len(plan.Manual) != 0 || len(plan.Failed) != 0 {
		t.Errorf("filtered=%d manual=%+v failed=%+v", plan.Filtered, plan.Manual, plan.Failed)
	}
	cs := plan.ChangeSets[0]
	if len(cs.Items) != 1 || cs.Items[0].To != "2.0" || len(cs.Items[0].Findings) != 1 || cs.Items[0].Findings[0].Rule != "CVE-2" {
		t.Errorf("security change set: %+v", cs.Items)
	}
	// Open requests of filtered groups are not closed; truly stale ones are.
	repo := sdktest.NewFakeRepo("main")
	app := sdk.Project{ID: "maven:app"}
	repo.CRs = append(repo.CRs,
		sdk.ChangeRequest{ID: "70", SourceBranch: "deepward/maven/app/updates-other", State: "open", Body: Body(ChangeSet{Project: app, Group: "updates/g:other"})},
		sdk.ChangeRequest{ID: "71", SourceBranch: "deepward/maven/app/security-both", State: "open", Body: Body(ChangeSet{Project: app, Group: "security/g:both"})},
		sdk.ChangeRequest{ID: "72", SourceBranch: "deepward/maven/app/old", State: "open", Body: Body(ChangeSet{Project: app, Group: "old"})})
	res, err := e.Publish(context.Background(), repo, plan, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 1 || res.Closed[0].ID != "72" {
		t.Errorf("closed: %+v", res.Closed)
	}
	if len(res.Created) != 1 || res.Created[0].SourceBranch != "deepward/maven/app/security-lib" {
		t.Errorf("created: %+v", res.Created)
	}
}

func TestFixKinds(t *testing.T) {
	plan := planWith(t, "lib=1.0\nother=1.0\n", "fix:\n  kinds: [outdated]\n")
	if got := branches(plan); got != "deepward/maven/app/updates-other" || plan.Filtered != 2 {
		t.Errorf("only updates: %s, filtered %d", got, plan.Filtered)
	}
	plan = planWith(t, "lib=1.0\nother=1.0\n", "fix:\n  kinds: [vulnerability]\n  minSeverity: high\n")
	if got := branches(plan); got != "deepward/maven/app/security-lib" || plan.Filtered != 1 {
		t.Errorf("vulnerabilities at or above high: %s, filtered %d", got, plan.Filtered)
	}
}

func TestFixConcurrencyKeepsResults(t *testing.T) {
	content := "lib=1.0\nother=1.0\nnofix=1.0\nbroken=1.0\nunrelated=1.0\ntext=1.0\nboth=1.0\n"
	summary := func(cfg string) string {
		plan := planWith(t, content, cfg)
		var sb strings.Builder
		for _, cs := range plan.ChangeSets {
			sb.WriteString(cs.Branch + "\n" + cs.Diff)
		}
		for _, m := range plan.Manual {
			sb.WriteString("manual " + m.Group + "\n")
		}
		for _, f := range plan.Failed {
			sb.WriteString("failed " + f.Group + "\n")
		}
		return sb.String()
	}
	one := summary("fix:\n  concurrency: 1\n")
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		for _, n := range []string{"1", "8"} {
			// Without affected packages every group is verified on its own,
			// so both modes produce the same plan, in the same order.
			if got := summary("fix:\n  verify: " + mode + "\n  concurrency: " + n + "\n"); got != one {
				t.Errorf("results depend on mode or concurrency (%s, %s):\n--- default, 1 ---\n%s\n--- got ---\n%s", mode, n, one, got)
			}
		}
	}
	// A reused copy is restored between groups: no change set carries
	// another group's edit.
	plan := planWith(t, content, "fix:\n  concurrency: 1\n")
	for _, cs := range plan.ChangeSets {
		if strings.Count(cs.Diff, "\n+") > 2 && !strings.Contains(cs.Branch, "security-both") {
			t.Errorf("%s carries more than its own edit:\n%s", cs.Branch, cs.Diff)
		}
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent writers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestProgressLogs(t *testing.T) {
	run := func(cfg string) string {
		var buf syncBuffer
		eco := &manifestEco{sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml"}}
		eco.RealizeFunc = realizer
		reg := sdk.NewRegistry()
		_ = reg.Register(eco)
		_ = reg.Register(fixAnalyzer(t))
		e, err := New(context.Background(), Options{Registry: reg, Config: parseConfig(t, cfg), Workspace: fixWorkspace(t, "lib=1.0\nbroken=1.0\nunrelated=1.0\n"),
			Logger: slog.New(slog.NewTextHandler(&buf, nil))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.PlanFixes(context.Background()); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	out := run("")
	for _, want := range []string{"projects detected", "analyzer finished", "scan finished", "verifying fixes", "verify=batch", "verifying batch",
		"verifying change", "change rejected", "change verified", "fix group done", "fixes verified", "graphBuilds=", "duration="} {
		if !strings.Contains(out, want) {
			t.Errorf("progress lacks %q:\n%s", want, out)
		}
	}
	each := run("fix:\n  verify: each\n")
	for _, want := range []string{"verify=each", "verifying fix group", "verifying change", "change rejected", "change verified", "fix group done", "graphBuilds="} {
		if !strings.Contains(each, want) {
			t.Errorf("each: progress lacks %q:\n%s", want, each)
		}
	}
	// Verification scans are nested: only the main scan reports itself.
	if n := strings.Count(out, "scan finished"); n != 1 {
		t.Errorf("scan finished logged %d times:\n%s", n, out)
	}
	if out := run("log:\n  progress: false\n"); strings.Contains(out, "verifying") || strings.Contains(out, "scan finished") {
		t.Errorf("progress disabled, got:\n%s", out)
	}
}

func TestFixAndLogConfigValidation(t *testing.T) {
	for _, bad := range []string{
		"fix:\n  minSeverity: urgent\n", "fix:\n  kinds: ['']\n", "fix:\n  concurrency: -1\n",
		"log:\n  level: loud\n", "log:\n  format: xml\n",
	} {
		var c Config
		if err := decodeConfigForTest(bad, &c); err != nil {
			continue
		}
		if c.Validate() == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	var c Config
	if err := decodeConfigForTest("fix:\n  minSeverity: high\n  kinds: [vulnerability]\n  concurrency: 3\nlog:\n  level: debug\n  format: json\n  progress: false\n", &c); err != nil || c.Validate() != nil {
		t.Errorf("valid config rejected: %v %v", err, c.Validate())
	}
	if c.Log.ProgressEnabled() || !(LogConfig{}).ProgressEnabled() {
		t.Error("progress defaults to true and can be turned off")
	}
}

func decodeConfigForTest(src string, c *Config) error {
	doc, err := strictyaml.Parse([]byte(src), "deepward.yaml")
	if err != nil {
		return err
	}
	return strictyaml.Decode(doc, c, "", "deepward.yaml")
}

func TestTransitiveOrder(t *testing.T) {
	alts := []sdk.Remediation{pkgChange("parent", "1.0", "1.1", false), pkgChange("parent", "1.0", "1.2", false),
		pkgChange("child", "2.0", "2.1", true), {Description: "text edit"}}
	order := func(cfg string) string {
		e := fixEngine(t, fixWorkspace(t, ""), cfg, true)
		var out []string
		for _, r := range e.ordered(alts) {
			if r.Package == nil {
				out = append(out, "text")
				continue
			}
			out = append(out, r.Package.To)
		}
		return strings.Join(out, ",")
	}
	if got := order(""); got != "1.1,1.2,text,2.1" {
		t.Errorf("default upgrades first: %s", got)
	}
	if got := order("fix:\n  transitive: pin\n"); got != "2.1,1.1,1.2,text" {
		t.Errorf("pin: %s", got)
	}
	if got := order("fix:\n  transitive: upgrade\n"); got != "1.1,1.2,text,2.1" {
		t.Errorf("upgrade keeps upgrades first: %s", got)
	}
	var c Config
	if err := decodeConfigForTest("fix:\n  transitive: bump\n", &c); err == nil && c.Validate() == nil {
		t.Error("unknown fix.transitive must be rejected")
	}
}

func TestVerifyConfig(t *testing.T) {
	if got := (FixConfig{}).VerifyMode(); got != VerifyBatch {
		t.Errorf("default: %s", got)
	}
	for _, mode := range []string{VerifyBatch, VerifyEach} {
		var c Config
		if err := decodeConfigForTest("fix:\n  verify: "+mode+"\n", &c); err != nil || c.Validate() != nil || c.Fix.VerifyMode() != mode {
			t.Errorf("%s: %v %v %s", mode, err, c.Validate(), c.Fix.VerifyMode())
		}
	}
	var c Config
	if err := decodeConfigForTest("fix:\n  verify: all\n", &c); err == nil && c.Validate() == nil {
		t.Error("unknown fix.verify must be rejected")
	}
	c = Config{Fix: FixConfig{Verify: "all"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "fix.verify") {
		t.Errorf("validation: %v", err)
	}
}
