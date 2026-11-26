package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestBranchTemplate(t *testing.T) {
	for _, ok := range []string{DefaultBranchTemplate, "bot/{group}", "deps/{project}-{group}", "x/{ecosystem}/{group}"} {
		if err := ValidateBranchTemplate(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	bad := map[string]string{
		"deepward/{ecosystem}":         "must contain {group}",
		"deepward/{pkg}/{group}":       "unknown placeholder {pkg}",
		"{group}":                      "fixed prefix",
		"/x/{group}":                   "fixed prefix",
		"deep ward/{group}":            "not a valid branch name",
		"deepward/../{group}":          "not a valid branch name",
		"deepward//{group}":            "not a valid branch name",
		"-x/{group}":                   "fixed prefix",
		"deepward/{ecosystem}{group}~": "not a valid branch name",
	}
	for tmpl, want := range bad {
		if err := ValidateBranchTemplate(tmpl); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", tmpl, err, want)
		}
	}
	cases := []struct{ tmpl, eco, dir, group, want string }{
		{DefaultBranchTemplate, "npm", ".", "security/axios", "deepward/npm/security-axios"},
		{DefaultBranchTemplate, "npm", "web/Legacy App", "security/axios", "deepward/npm/web/legacy-app/security-axios"},
		{DefaultBranchTemplate, "go", `services\api`, "all", "deepward/go/services/api/all"},
		{"bot/{project}/{group}", "maven", "", "updates/g:lib", "bot/updates-lib"},
	}
	for _, c := range cases {
		if got := renderBranch(c.tmpl, c.eco, dirSlug(c.dir), Slug(c.group)); got != c.want {
			t.Errorf("renderBranch(%q, %q, %q) = %q, want %q", c.tmpl, c.dir, c.group, got, c.want)
		}
	}
	if branchPrefix(DefaultBranchTemplate) != "deepward/" || branchPrefix("fixed") != "fixed" {
		t.Error("branchPrefix")
	}
}

func TestBranchConfigValidation(t *testing.T) {
	for _, cfg := range []string{"fix:\n  branch: \"{group}\"\n", "fix:\n  grouping: batch\n"} {
		c := parseConfig(t, cfg)
		if err := c.Validate(); err == nil {
			t.Errorf("%q must be rejected", cfg)
		}
	}
}

func TestBranchCollision(t *testing.T) {
	e := &Engine{cfg: Config{Fix: FixConfig{Branch: "bot/{group}"}}}
	cs := []ChangeSet{
		{Project: sdk.Project{ID: "npm:a"}, Group: "security/axios", Branch: "bot/security-axios"},
		{Project: sdk.Project{ID: "npm:b"}, Group: "security/axios", Branch: "bot/security-axios"},
	}
	err := e.checkBranches(cs)
	if err == nil || !strings.Contains(err.Error(), "npm:a") || !strings.Contains(err.Error(), "add {project}") {
		t.Errorf("collision: %v", err)
	}
	if err := e.checkBranches(cs[:1]); err != nil {
		t.Error(err)
	}
}

func planWith(t *testing.T, content, cfg string) *FixPlan {
	t.Helper()
	plan, err := fixEngine(t, fixWorkspace(t, content), cfg, true).PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestUpdateMergedIntoSecurityFix(t *testing.T) {
	plan := planWith(t, "both=1.0\nstuck=1.0\n", "")
	if len(plan.ChangeSets) != 2 {
		t.Fatalf("one request per package, the updates merged: %+v", plan.ChangeSets)
	}
	byGroup := map[string]ChangeSet{}
	for _, cs := range plan.ChangeSets {
		byGroup[cs.Group] = cs
	}
	both := byGroup["security/g:both"]
	if len(both.Items) != 1 || both.Items[0].To != "1.9" || !both.Items[0].Upgrade || len(both.Items[0].Findings) != 2 ||
		both.Title != "Deepward: fix vulnerabilities: g:both 1.0 → 1.9 in app" || both.Branch != "deepward/maven/app/security-both" {
		t.Errorf("security fix carries the update: %+v", both)
	}
	if body := Body(both); !strings.Contains(body, "| security fix, minor update |") {
		t.Errorf("body type:\n%s", body)
	}
	stuck := byGroup["security/g:stuck"]
	if len(stuck.Items) != 1 || stuck.Items[0].To != "1.5" || stuck.Items[0].Upgrade {
		t.Errorf("the update reintroduces the vulnerability, so the security fix stands alone: %+v", stuck)
	}
	if len(plan.Failed) != 1 || plan.Failed[0].Group != "updates/g:stuck" || !strings.Contains(plan.Failed[0].Reason, "CVE-stuck is still reported") {
		t.Errorf("the update is reported under its own group: %+v", plan.Failed)
	}
	for _, cs := range plan.ChangeSets {
		if strings.HasPrefix(cs.Group, "updates/") {
			t.Errorf("no separate update request: %+v", cs)
		}
	}
}

func TestUpgradeManual(t *testing.T) {
	g := &fixGroup{project: sdk.Project{ID: "maven:app"}, name: "security/g:lib"}
	vuln := &target{findings: []sdk.Finding{{Kind: sdk.KindVulnerability}}, alternatives: []sdk.Remediation{pkgChange("lib", "1.0", "2.0", false)}}
	pinned := &target{findings: []sdk.Finding{{Kind: sdk.KindVulnerability}}, alternatives: []sdk.Remediation{pkgChange("x", "1.0", "2.0", true)}}
	upd := pkgChange("lib", "1.0", "3.0", false)
	other := pkgChange("other", "1.0", "3.0", false)
	ug := &fixGroup{project: g.project, name: "updates/g:lib", targets: []*target{
		{findings: []sdk.Finding{{Kind: sdk.KindOutdated}}, alternatives: []sdk.Remediation{upd}},
		{findings: []sdk.Finding{{Kind: sdk.KindOutdated}}, alternatives: []sdk.Remediation{other}},
	}}
	g.targets = []*target{vuln, pinned}
	out := mergeUpdates([]*fixGroup{g, ug})
	if len(out) != 2 || vuln.upgrade == nil || vuln.upgrade.Package.To != "3.0" || vuln.upgradeGroup != "updates/g:lib" || len(ug.targets) != 1 || pinned.upgrade != nil {
		t.Errorf("merge: %+v %+v", vuln, ug.targets)
	}
	manual, failed := report(nil, nil, "p", "g", sdk.Finding{Rule: "r"}, &sdk.ManualChange{Reason: "by hand"}, "")
	if len(manual) != 1 || len(failed) != 0 || manual[0].Reason != "by hand" {
		t.Errorf("report manual: %+v %+v", manual, failed)
	}
	if m, f := report(nil, nil, "p", "g", sdk.Finding{}, nil, ""); m != nil || f != nil {
		t.Error("nothing to report")
	}
}

func TestGroupingStrategies(t *testing.T) {
	content := "lib=1.0\nboth=1.0\nother=1.0\ntext=1.0\n"
	plan := planWith(t, content, "fix:\n  grouping: type\n")
	branches := map[string]ChangeSet{}
	for _, cs := range plan.ChangeSets {
		branches[cs.Branch] = cs
	}
	sec, upd, code := branches["deepward/maven/app/security"], branches["deepward/maven/app/updates"], branches["deepward/maven/app/vulns"]
	if len(plan.ChangeSets) != 3 || len(sec.Items) != 2 || sec.Title != "Deepward: fix vulnerabilities in 2 packages in app" ||
		len(upd.Items) != 1 || upd.Title != "Deepward: update g:other 1.0 → 1.1 in app" || len(code.Items) != 1 {
		t.Fatalf("type grouping: %+v", plan.ChangeSets)
	}
	plan = planWith(t, content, "fix:\n  grouping: project\n")
	if len(plan.ChangeSets) != 1 {
		t.Fatalf("project grouping: %+v", plan.ChangeSets)
	}
	all := plan.ChangeSets[0]
	if all.Branch != "deepward/maven/app/all" || len(all.Items) != 4 || !all.Security ||
		all.Title != "Deepward: fix vulnerabilities in 2 packages, update 1 dependency, 1 other change in app" {
		t.Errorf("project grouping: %s %s %d", all.Branch, all.Title, len(all.Items))
	}
	plan = planWith(t, "other=1.0\ntext=1.0\n", "fix:\n  grouping: project\n")
	if plan.ChangeSets[0].Title != "Deepward: update 1 dependency, 1 other change in app" {
		t.Errorf("no security: %s", plan.ChangeSets[0].Title)
	}
	plan = planWith(t, content, "fix:\n  grouping: project\n  groups:\n    - name: core\n      match: [\"pkg:maven/g/lib\", \"pkg:maven/g/both\"]\n")
	for _, cs := range plan.ChangeSets {
		if cs.Group == "core" && cs.Title != "Deepward: fix vulnerabilities: core (2 changes) in app" {
			t.Errorf("group rules keep their title: %s", cs.Title)
		}
	}
	if len(plan.ChangeSets) != 2 {
		t.Errorf("rules win over the strategy: %+v", plan.ChangeSets)
	}
}

func TestCloseSupersededRequest(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\nother=1.0\n")
	e := fixEngine(t, ws, "fix:\n  grouping: project\n", true)
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	repo := sdktest.NewFakeRepo("main")
	old := ChangeSet{Project: sdk.Project{ID: "maven:app"}, Group: "updates/g:other",
		Items: []FixItem{{Package: "pkg:maven/g/other", From: "1.0", To: "1.1"}}}
	gone := ChangeSet{Project: sdk.Project{ID: "maven:app"}, Group: "updates/g:gone",
		Items: []FixItem{{Package: "pkg:maven/g/gone", From: "1.0", To: "1.1"}}}
	repo.CRs = append(repo.CRs,
		sdk.ChangeRequest{ID: "50", SourceBranch: "deepward/maven/app/updates-other", State: "open", Body: Body(old)},
		sdk.ChangeRequest{ID: "51", SourceBranch: "deepward/maven/app/updates-gone", State: "open", Body: Body(gone)})
	res, err := e.Publish(context.Background(), repo, plan, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 2 {
		t.Fatalf("closed: %+v", res.Closed)
	}
	if c := repo.Comments["50"]; len(c) != 1 || !strings.Contains(c[0], `now part of "Deepward: fix vulnerabilities in 1 package, update 1 dependency in app"`) {
		t.Errorf("superseded comment: %v", c)
	}
	if c := repo.Comments["51"]; len(c) != 1 || !strings.Contains(c[0], "no longer needed") {
		t.Errorf("stale comment: %v", c)
	}
}
