package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

var versionLine = regexp.MustCompile(`(?m)^(\w+)=([0-9.]+)$`)

func versions(t *testing.T, fsys fs.FS) map[string]string {
	data, err := fs.ReadFile(fsys, "app/versions.txt")
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, m := range versionLine.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2]
	}
	return out
}

func pkgChange(name, from, to string, pin bool) sdk.Remediation {
	return sdk.Remediation{Description: name, Group: "security/g:" + name,
		Package: &sdk.PackageChange{PURL: "pkg:maven/g/" + name, From: from, To: to, Pin: pin}}
}

// fixAnalyzer reports findings based on app/versions.txt in the workspace
// being analyzed (the original or a scratch copy).
func fixAnalyzer(t *testing.T) *sdktest.FakeAnalyzer {
	return &sdktest.FakeAnalyzer{ID: "vulns", Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		v := versions(t, req.Workspace.FS)
		var out []sdk.Finding
		mk := func(rule, name string, kind sdk.FindingKind, sev sdk.Severity, rems ...sdk.Remediation) {
			out = append(out, sdk.Finding{Rule: rule, Kind: kind, Severity: sev, Message: rule + " in " + name, Title: "issue in " + name,
				References:   []sdk.Reference{{URL: "https://osv.dev/vulnerability/" + rule}},
				Locations:    []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/g/" + name + "@" + v[name], Direct: true}}},
				Remediations: rems, Properties: map[string]any{"updateType": "minor", "releaseNotes": "https://example.com/" + name}})
		}
		if v["lib"] != "" && v["lib"] < "2.0" {
			mk("CVE-1", "lib", sdk.KindVulnerability, sdk.SeverityHigh, pkgChange("lib", v["lib"], "1.5", false), pkgChange("lib", v["lib"], "2.0", false))
			mk("CVE-2", "lib", sdk.KindVulnerability, sdk.SeverityCritical, pkgChange("lib", v["lib"], "1.5", false), pkgChange("lib", v["lib"], "2.0", false))
		}
		if v["other"] == "1.0" {
			r := pkgChange("other", "1.0", "1.1", false)
			r.Group = "updates/g:other"
			mk("update-minor", "other", sdk.KindOutdated, sdk.SeverityNone, r)
		}
		// "both" has a vulnerability fixed in 1.5 and an update to 1.9;
		// "stuck" is vulnerable again in 3.0, the version its update proposes.
		for _, name := range []string{"both", "stuck"} {
			cur := v[name]
			if cur == "" {
				continue
			}
			if cur < "1.5" || (name == "stuck" && cur >= "3.0") {
				mk("CVE-"+name, name, sdk.KindVulnerability, sdk.SeverityMedium, pkgChange(name, cur, "1.5", false))
			}
			latest := map[string]string{"both": "1.9", "stuck": "3.0"}[name]
			if cur < latest {
				r := pkgChange(name, cur, latest, false)
				r.Group = "updates/g:" + name
				mk("update-minor", name, sdk.KindOutdated, sdk.SeverityNone, r)
			}
		}
		if v["nofix"] != "" {
			mk("CVE-3", "nofix", sdk.KindVulnerability, sdk.SeverityMedium, pkgChange("nofix", "1.0", "2.0", true))
		}
		if v["broken"] != "" {
			mk("CVE-4", "broken", sdk.KindVulnerability, sdk.SeverityLow, pkgChange("broken", "1.0", "2.0", false))
		}
		raw, _ := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
		if v["text"] == "1.0" && !strings.HasPrefix(string(raw), "# fixed") {
			out = append(out, sdk.Finding{Rule: "text-rule", Kind: sdk.KindCodeIssue, Severity: sdk.SeverityLow, Message: "text",
				Locations:    []sdk.Location{{File: &sdk.FileLocation{Path: "app/versions.txt"}}},
				Remediations: []sdk.Remediation{{Description: "bump text", TextEdits: []sdk.TextEdit{{Path: "app/versions.txt", Start: 0, End: 0, Replacement: "# fixed\n"}}}}})
		}
		return out, nil
	}}
}

// realizer edits app/versions.txt; "nofix" needs a human, "broken" edits a
// line that does not affect the finding.
func realizer(_ context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	data, err := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	var res sdk.RealizeResult
	for _, c := range req.Changes {
		name := c.PURL[strings.LastIndex(c.PURL, "/")+1:]
		switch name {
		case "nofix":
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "managed outside the repository", Suggestion: "pin it by hand"})
			continue
		case "broken":
			name = "unrelated"
		}
		loc := regexp.MustCompile(`(?m)^` + name + `=([0-9.]+)$`).FindSubmatchIndex(data)
		if loc == nil {
			continue
		}
		res.Edits = append(res.Edits, sdk.TextEdit{Path: "app/versions.txt", Start: loc[2], End: loc[3], Replacement: c.To})
	}
	return res, nil
}

func fixWorkspace(t *testing.T, content string) *sdk.Workspace {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "app", "target"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "app", "pom.xml"), []byte("<project/>"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "app", "versions.txt"), []byte(content), 0o644)
	_ = os.WriteFile(filepath.Join(root, "app", "target", "big.bin"), []byte("ignored"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "shared.props"), []byte("shared"), 0o644)
	return sdk.NewWorkspace(root, nil)
}

type manifestEco struct{ sdktest.FakeEcosystem }

func (m *manifestEco) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	ps, err := m.FakeEcosystem.Detect(ctx, ws)
	for i := range ps {
		ps[i].Manifests = append(ps[i].Manifests, "shared.props")
	}
	return ps, err
}

func fixEngine(t *testing.T, ws *sdk.Workspace, cfg string, withRealizer bool) *Engine {
	t.Helper()
	eco := &manifestEco{sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml"}}
	if withRealizer {
		eco.RealizeFunc = realizer
	}
	reg := sdk.NewRegistry()
	_ = reg.Register(eco)
	_ = reg.Register(fixAnalyzer(t))
	e, err := New(context.Background(), Options{Registry: reg, Config: parseConfig(t, cfg), Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPlanFixes(t *testing.T) {
	original := "lib=1.0\nother=1.0\nnofix=1.0\nbroken=1.0\nunrelated=1.0\ntext=1.0\n"
	ws := fixWorkspace(t, original)
	e := fixEngine(t, ws, "", true)
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangeSets) != 3 || len(plan.Manual) != 1 || len(plan.Failed) != 1 {
		t.Fatalf("plan: changeSets=%+v manual=%+v failed=%+v", plan.ChangeSets, plan.Manual, plan.Failed)
	}
	sec := plan.ChangeSets[0]
	if !sec.Security || sec.MaxSeverity != sdk.SeverityCritical || sec.Branch != "deepward/maven/app/security-lib" || len(sec.Items) != 1 || sec.Items[0].To != "2.0" {
		t.Errorf("security change set (1.5 must be rejected by verification): %+v", sec)
	}
	if !strings.Contains(sec.Diff, "-lib=1.0\n+lib=2.0") || len(sec.Files) != 1 || sec.Title != "Deepward: fix vulnerabilities: g:lib 1.0 → 2.0 in app" {
		t.Errorf("diff/files/title:\n%s\n%s", sec.Diff, sec.Title)
	}
	if strings.Join(sec.Labels, ",") != "dependencies,security" {
		t.Errorf("labels: %v", sec.Labels)
	}
	if plan.Manual[0].Reason != "managed outside the repository" || plan.Manual[0].Suggestion == "" || plan.Manual[0].Package == "" {
		t.Errorf("manual: %+v", plan.Manual[0])
	}
	if !strings.HasPrefix(plan.Failed[0].Reason, "verification-failed: CVE-4 is still reported") {
		t.Errorf("failed: %+v", plan.Failed[0])
	}
	var outdated, text *ChangeSet
	for i := range plan.ChangeSets {
		switch {
		case strings.HasSuffix(plan.ChangeSets[i].Branch, "updates-other"):
			outdated = &plan.ChangeSets[i]
		case plan.ChangeSets[i].Items[0].Text:
			text = &plan.ChangeSets[i]
		}
	}
	if outdated == nil || outdated.Security || strings.Join(outdated.Labels, ",") != "dependencies" {
		t.Errorf("outdated change set: %+v", outdated)
	}
	if text == nil || !strings.Contains(text.Diff, "+# fixed") || !strings.Contains(text.Title, "(1 changes)") {
		t.Errorf("text-edit change set: %+v", text)
	}
	if data, _ := os.ReadFile(filepath.Join(ws.Root, "app", "versions.txt")); string(data) != original {
		t.Error("the real workspace must not be modified")
	}
}

func TestFixGroupsAndConfig(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\nother=1.0\n")
	e := fixEngine(t, ws, `fix:
  branch: "bot/{ecosystem}/{group}"
  labels: [deps]
  securityLabels: [sec]
  groups:
    - name: all-g
      match: ["pkg:maven/g/*"]
`, true)
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangeSets) != 1 || plan.ChangeSets[0].Branch != "bot/maven/all-g" || len(plan.ChangeSets[0].Items) != 2 ||
		strings.Join(plan.ChangeSets[0].Labels, ",") != "deps,sec" {
		t.Fatalf("grouped: %+v", plan.ChangeSets)
	}
}

// plainEco detects projects but cannot edit manifests.
type plainEco struct{ inner *sdktest.FakeEcosystem }

func (p plainEco) Info() sdk.Info { return p.inner.Info() }

func (p plainEco) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	return p.inner.Detect(ctx, ws)
}

func TestFixWithoutRealizerOrChanges(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\n")
	plan, err := fixEngine(t, ws, "", false).PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangeSets) != 0 || len(plan.Manual) != 1 || !strings.Contains(plan.Manual[0].Reason, "could not edit the manifests: not supported") {
		t.Errorf("realizer error: %+v", plan)
	}
	reg := sdk.NewRegistry()
	_ = reg.Register(plainEco{&sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml"}})
	_ = reg.Register(fixAnalyzer(t))
	e, _ := New(context.Background(), Options{Registry: reg, Workspace: ws})
	plan, _ = e.PlanFixes(context.Background())
	if len(plan.Manual) != 1 || !strings.Contains(plan.Manual[0].Reason, "cannot edit manifests") {
		t.Errorf("no realizer: %+v", plan.Manual)
	}
	clean := fixWorkspace(t, "lib=2.0\n")
	plan, _ = fixEngine(t, clean, "", true).PlanFixes(context.Background())
	if len(plan.ChangeSets) != 0 || len(plan.Manual) != 0 {
		t.Errorf("nothing to fix: %+v", plan)
	}
	if _, err := (&Engine{}).PlanFixes(context.Background()); err == nil {
		t.Error("no workspace")
	}
}

func TestSlugAndNames(t *testing.T) {
	cases := map[string]string{"security/org.apache.logging.log4j:log4j-core": "security-log4j-core", "jackson": "jackson",
		"Updates/G:Weird Name!": "updates-weird-name", ":::": "changes"}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if packageName("pkg:maven/org.apache/log4j-core") != "org.apache:log4j-core" || packageName("pkg:nuget/newtonsoft.json") != "newtonsoft.json" {
		t.Error("packageName")
	}
}

func TestPublish(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\nother=1.0\n")
	limit := 0
	e := fixEngine(t, ws, "", true)
	e.cfg.Fix.Limit = &limit
	plan, _ := e.PlanFixes(context.Background())
	repo := sdktest.NewFakeRepo("main")
	stale := sdk.ChangeRequest{ID: "90", SourceBranch: "deepward/maven/old", State: "open",
		Body: Body(ChangeSet{Project: sdk.Project{ID: "maven:app"}, Group: "old"})}
	foreign := sdk.ChangeRequest{ID: "91", SourceBranch: "deepward/maven/foreign", State: "open", Body: "no marker"}
	otherProject := sdk.ChangeRequest{ID: "92", SourceBranch: "deepward/maven/elsewhere", State: "open",
		Body: Body(ChangeSet{Project: sdk.Project{ID: "maven:gone"}, Group: "x"})}
	repo.CRs = append(repo.CRs, stale, foreign, otherProject)
	res, err := e.Publish(context.Background(), repo, plan, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 || len(res.Deferred) != 1 || res.Created[0].SourceBranch != "deepward/maven/app/security-lib" {
		t.Fatalf("limit 0: security bypasses, update deferred: %+v", res)
	}
	if len(res.Closed) != 1 || res.Closed[0].ID != "90" {
		t.Errorf("stale request closed (only bot requests of scanned projects): %+v", res.Closed)
	}
	body := repo.CRs[len(repo.CRs)-1].Body
	for _, want := range []string{"| `g:lib` | 1.0 → **2.0** | security fix |", "[CVE-1](https://osv.dev/vulnerability/CVE-1) (high)", "### Release notes", "deepward:meta"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if repo.CRs[len(repo.CRs)-1].Labels[1] != "security" {
		t.Error("labels on the request")
	}
	// Second run: the existing request is updated, not duplicated.
	limit = 10
	res, _ = e.Publish(context.Background(), repo, plan, "main")
	if len(res.Updated) != 1 || len(res.Created) != 1 {
		t.Errorf("second run: %+v", res)
	}
	// A human commit pauses the request (commented once).
	repo.Commits["deepward/maven/app/security-lib"] = append(repo.Commits["deepward/maven/app/security-lib"], sdk.CommitInfo{SHA: "h1", AuthorName: "Dev"})
	res, _ = e.Publish(context.Background(), repo, plan, "main")
	res2, _ := e.Publish(context.Background(), repo, plan, "main")
	var secID string
	for _, cr := range repo.CRs {
		if cr.SourceBranch == "deepward/maven/app/security-lib" {
			secID = cr.ID
		}
	}
	if len(res.Paused) != 1 || len(res2.Paused) != 1 || len(repo.Comments[secID]) != 1 {
		t.Errorf("paused: %+v comments=%v", res.Paused, repo.Comments)
	}
	if mk, ok := ParseMarker(repo.CRs[len(repo.CRs)-2].Body); ok && mk.Group == "" {
		t.Error("marker")
	}
}

type failingRepo struct {
	*sdktest.FakeRepo
	fail string
}

func (f *failingRepo) ListChangeRequests(ctx context.Context, p string) ([]sdk.ChangeRequest, error) {
	if f.fail == "list" {
		return nil, errors.New("boom")
	}
	return f.FakeRepo.ListChangeRequests(ctx, p)
}

func (f *failingRepo) Commit(ctx context.Context, r sdk.CommitRequest) (string, error) {
	if f.fail == "commit" {
		return "", errors.New("boom")
	}
	return f.FakeRepo.Commit(ctx, r)
}

func (f *failingRepo) CreateChangeRequest(ctx context.Context, s sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	if f.fail == "create" {
		return sdk.ChangeRequest{}, errors.New("boom")
	}
	return f.FakeRepo.CreateChangeRequest(ctx, s)
}

func (f *failingRepo) BranchCommits(ctx context.Context, b, base string) ([]sdk.CommitInfo, error) {
	if f.fail == "commits" {
		return nil, errors.New("boom")
	}
	return f.FakeRepo.BranchCommits(ctx, b, base)
}

func (f *failingRepo) CloseChangeRequest(ctx context.Context, id string) error {
	if f.fail == "close" {
		return errors.New("boom")
	}
	return f.FakeRepo.CloseChangeRequest(ctx, id)
}

func TestPublishErrors(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\n")
	e := fixEngine(t, ws, "fix:\n  branch: bot/{ecosystem}/{group}\n", true)
	plan, _ := e.PlanFixes(context.Background())
	for _, fail := range []string{"list", "commit", "create"} {
		r := &failingRepo{FakeRepo: sdktest.NewFakeRepo("main"), fail: fail}
		if _, err := e.Publish(context.Background(), r, plan, "main"); err == nil {
			t.Errorf("%s failure must propagate", fail)
		}
	}
	r := &failingRepo{FakeRepo: sdktest.NewFakeRepo("main")}
	_, _ = e.Publish(context.Background(), r, plan, "main")
	for _, fail := range []string{"commits", "commit"} {
		r.fail = fail
		if _, err := e.Publish(context.Background(), r, plan, "main"); err == nil {
			t.Errorf("update path %s failure must propagate", fail)
		}
	}
	r.fail = "close"
	r.CRs = append(r.CRs, sdk.ChangeRequest{ID: "77", SourceBranch: "bot/maven/old", State: "open", Body: Body(ChangeSet{Project: sdk.Project{ID: "maven:app"}, Group: "old"})})
	if _, err := e.Publish(context.Background(), r, plan, "main"); err == nil {
		t.Error("close failure must propagate")
	}
	if _, ok := ParseMarker("<!-- deepward:meta {bad} -->"); ok {
		t.Error("bad marker")
	}
	if Body(ChangeSet{Items: []FixItem{{Text: true, Package: "edit"}, {Package: "pkg:maven/a/b", Kind: sdk.KindOutdated}}}) == "" {
		t.Error("body")
	}
}
