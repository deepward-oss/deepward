package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestTitleConfigDecoding(t *testing.T) {
	var c Config
	err := decodeConfigForTest("fix:\n  title:\n    bugfix: \"fix: {name}\"\n", &c)
	if err == nil || !strings.Contains(err.Error(), "bugfix") {
		t.Errorf("unknown kind must be rejected by name: %v", err)
	}
	if err := decodeConfigForTest("fix:\n  title:\n    security: \"fix(deps): {name}\"\n    pin: \"pin {name}\"\n    update: \"chore: {name}\"\n    group: \"chore: {group}\"\n", &c); err != nil || c.Validate() != nil {
		t.Errorf("valid patterns rejected: %v %v", err, c.Validate())
	}
}

func TestTitlePatternValidation(t *testing.T) {
	cases := map[string][]string{
		"fix:\n  title:\n    update: \"chore(deps): bump {version}\"\n": {"fix.title.update", "{version}"},
		"fix:\n  title:\n    security: \"{project}\"\n":                 {"fix.title.security", "empty title"},
		"fix:\n  title:\n    group: \"  in {project}  \"\n":             {"fix.title.group", "empty title"},
	}
	for src, want := range cases {
		var c Config
		if err := decodeConfigForTest(src, &c); err != nil {
			t.Fatal(err)
		}
		err := c.Validate()
		for _, w := range want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("%q: error %v lacks %q", src, err, w)
			}
		}
	}
}

func vulnFinding(sev sdk.Severity) sdk.Finding {
	return sdk.Finding{Kind: sdk.KindVulnerability, Severity: sev, Rule: "CVE-1"}
}

func TestRenderTitles(t *testing.T) {
	kafka := FixItem{Package: "pkg:maven/org.apache.kafka/kafka-clients", From: "4.4.0", To: "4.5.0", Kind: sdk.KindVulnerability,
		Findings: []sdk.Finding{vulnFinding(sdk.SeverityHigh)}}
	zk := FixItem{Package: "pkg:maven/org.apache.zookeeper/zookeeper", From: "3.9.3", To: "3.9.5", Pin: true, Kind: sdk.KindVulnerability,
		Findings: []sdk.Finding{vulnFinding(sdk.SeverityCritical)}}
	grpc := FixItem{Package: "pkg:maven/io.grpc/grpc-stub", From: "1.85.1", To: "1.86.0", Kind: sdk.KindOutdated, UpdateType: "minor"}
	root := sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: "."}
	api := sdk.Project{ID: "maven:services/api", Ecosystem: "maven", Dir: "services/api"}
	cs := func(p sdk.Project, group string, items ...FixItem) *ChangeSet {
		c := &ChangeSet{Project: p, Group: group, Items: items}
		for _, it := range items {
			c.Security = c.Security || it.Kind == sdk.KindVulnerability
		}
		return c
	}
	cases := []struct {
		title TitleConfig
		cs    *ChangeSet
		want  string
	}{
		{TitleConfig{Security: "fix(deps): bump {name} from {from} to {to}"}, cs(root, "security/org.apache.kafka:kafka-clients", kafka),
			"fix(deps): bump kafka-clients from 4.4.0 to 4.5.0"},
		// pin falls back to security
		{TitleConfig{Security: "fix(deps): bump {name} to {to}"}, cs(root, "security/zk", zk), "fix(deps): bump zookeeper to 3.9.5"},
		{TitleConfig{Security: "x", Pin: "fix(deps): pin {name} to {to}"}, cs(root, "security/zk", zk), "fix(deps): pin zookeeper to 3.9.5"},
		{TitleConfig{Group: "chore(deps): update {group} ({count} packages)"}, cs(root, "jackson", grpc, grpc, grpc),
			"chore(deps): update jackson (3 packages)"},
		// placeholders without a value in a group collapse
		{TitleConfig{Group: "chore: {name} {from} → {to} {group}"}, cs(root, "jackson", grpc, grpc), "chore: → jackson"},
		{TitleConfig{Security: "fix({project}): {name} {from} → {to}"}, cs(root, "s", kafka), "fix: kafka-clients 4.4.0 → 4.5.0"},
		{TitleConfig{Security: "fix([{project}]) [{project}] {name}"}, cs(root, "s", kafka), "fix() kafka-clients"},
		{TitleConfig{Update: "Update {name} {from} → {to} in {project}"}, cs(root, "u", grpc), "Update grpc-stub 1.85.1 → 1.86.0"},
		{TitleConfig{Update: "Update {name} {from} → {to} in {project}"}, cs(api, "u", grpc), "Update grpc-stub 1.85.1 → 1.86.0 in services/api"},
		{TitleConfig{Update: "{name} IN {project} for x"}, cs(root, "u", grpc), "grpc-stub for x"},
		{TitleConfig{Update: "[{update}] {package}: {from} → {to} ({ecosystem})"}, cs(root, "u", grpc),
			"[minor] io.grpc:grpc-stub: 1.85.1 → 1.86.0 (maven)"},
		{TitleConfig{Security: "[{severity}] {package}"}, cs(root, "s", kafka, zk), "Deepward-default"},
		{TitleConfig{Group: "[{severity}] {count} fixes in {project}"}, cs(api, "security", kafka, zk), "[critical] 2 fixes in services/api"},
		// a kind without a pattern keeps the default title
		{TitleConfig{Update: "chore: {name}"}, cs(root, "s", kafka), "Deepward-default"},
		// text-only requests keep the default title
		{TitleConfig{Group: "chore: {group}", Update: "u"}, cs(root, "t", FixItem{Text: true, Package: "bump text"}), "Deepward-default"},
	}
	for i, c := range cases {
		e := fixEngine(t, fixWorkspace(t, ""), "", true)
		e.cfg.Fix.Title = c.title
		got, ok := e.customTitle(c.cs)
		if !ok {
			got = "Deepward-default"
		}
		if got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}

func TestTitleMarkersAndLength(t *testing.T) {
	e := fixEngine(t, fixWorkspace(t, ""), "fix:\n  title:\n    pin: \"fix(deps): pin {name} to {to}\"\n    update: \"chore: {name} "+strings.Repeat("x", 300)+"\"\n", true)
	pin := &ChangeSet{Project: sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: "."}, Group: "security/g:lz4-java", Items: []FixItem{{
		Package: "pkg:maven/at.yawk.lz4/lz4-java", From: "1.11.2", To: "1.11.4", Pin: true, Unverified: true, Kind: sdk.KindVulnerability,
		Findings: []sdk.Finding{vulnFinding(sdk.SeverityHigh)}}}}
	e.describe(pin)
	if pin.Title != "[unverified] fix(deps): pin lz4-java to 1.11.4" || !pin.CustomTitle {
		t.Errorf("unverified pin: %q", pin.Title)
	}
	long := &ChangeSet{Project: sdk.Project{ID: "go:.", Ecosystem: "go", Dir: "."}, Group: "updates/x", Items: []FixItem{{
		Package: "pkg:golang/example.com/mod", From: "1.0", To: "1.1", Kind: sdk.KindVulnerability, Unverified: true,
		Findings: []sdk.Finding{{Kind: sdk.KindVulnerability, Severity: sdk.SeverityLow, NotBuilt: true}}}}}
	e.cfg.Fix.Title.Security = "fix: {name} " + strings.Repeat("y", 300)
	e.describe(long)
	if n := len([]rune(long.Title)); n != maxTitleLength || !strings.HasPrefix(long.Title, "[unverified] fix: mod yyy") ||
		!strings.HasSuffix(long.Title, "y… (not built)") {
		t.Errorf("long title (%d): %q", n, long.Title)
	}
	// Without patterns the default title is unchanged.
	plain := fixEngine(t, fixWorkspace(t, ""), "", true)
	pin.Title, pin.CustomTitle = "", false
	plain.describe(pin)
	if pin.Title != "Deepward: [unverified] fix vulnerabilities: at.yawk.lz4:lz4-java 1.11.2 → 1.11.4" || pin.CustomTitle {
		t.Errorf("default title: %q", pin.Title)
	}
}

// commitRepo records commit messages.
type commitRepo struct {
	*sdktest.FakeRepo
	messages []string
}

func (r *commitRepo) Commit(ctx context.Context, req sdk.CommitRequest) (string, error) {
	r.messages = append(r.messages, req.Message)
	return r.FakeRepo.Commit(ctx, req)
}

func TestCommitSubjectFollowsTitle(t *testing.T) {
	subject := func(cfg string) string {
		e := fixEngine(t, fixWorkspace(t, "lib=1.0\n"), cfg, true)
		plan, err := e.PlanFixes(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		repo := &commitRepo{FakeRepo: sdktest.NewFakeRepo("main")}
		if _, err := e.Publish(context.Background(), repo, plan, "main"); err != nil {
			t.Fatal(err)
		}
		if len(repo.messages) != 1 {
			t.Fatalf("commits: %v", repo.messages)
		}
		return strings.SplitN(repo.messages[0], "\n", 2)[0]
	}
	if got := subject("fix:\n  title:\n    security: \"fix(deps): bump {name} from {from} to {to}\"\n"); got != "fix(deps): bump lib from 1.0 to 2.0" {
		t.Errorf("pattern subject: %q", got)
	}
	if got := subject(""); got != "fix vulnerabilities: g:lib 1.0 → 2.0 in app" {
		t.Errorf("default subject: %q", got)
	}
}

func TestBodyListsModules(t *testing.T) {
	finding := func(modules ...string) sdk.Finding {
		var refs []sdk.ModuleRef
		for _, m := range modules {
			refs = append(refs, sdk.ModuleRef{ID: "module:" + m, Name: m, Scope: "runtime"})
		}
		return sdk.Finding{Rule: "CVE-1", Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh,
			Locations: []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/at.yawk.lz4/lz4-java@1.11.2", Modules: refs}}}}
	}
	cs := ChangeSet{Project: sdk.Project{ID: "maven:."}, Group: "security/lz4", Security: true, Items: []FixItem{{
		Package: "pkg:maven/at.yawk.lz4/lz4-java", From: "1.11.2", To: "1.11.4", Pin: true, Kind: sdk.KindVulnerability,
		Findings: []sdk.Finding{finding("wr-history-app", "wr-history-api"), finding("wr-history-api")}}}}
	body := Body(cs)
	if !strings.Contains(body, "| Package | Change | Type | Modules |") ||
		!strings.Contains(body, "| `at.yawk.lz4:lz4-java` | 1.11.2 → **1.11.4** | pin (transitive) | wr-history-api, wr-history-app |") {
		t.Errorf("modules column:\n%s", body)
	}
	cs.Items[0].Findings = []sdk.Finding{finding()}
	if body := Body(cs); strings.Contains(body, "Modules") || !strings.Contains(body, "| Package | Change | Type |\n") {
		t.Errorf("no modules column without modules:\n%s", body)
	}
}

func TestCloseRequestsOfMergedProjects(t *testing.T) {
	e := fixEngine(t, fixWorkspace(t, ""), "", true)
	plan := &FixPlan{Result: &sdk.ScanResult{Projects: []sdk.ProjectResult{
		{Project: sdk.Project{ID: "nuget:.", Ecosystem: "nuget", Dir: ".", Manifests: []string{"src/Core/Core.csproj", "src/Web/Web.csproj", "Directory.Packages.props"}}, Status: sdk.ProjectOK},
		{Project: sdk.Project{ID: "maven:src/Web", Ecosystem: "maven", Dir: "src/Web", Manifests: []string{"src/Web/pom.xml"}}, Status: sdk.ProjectFailed},
	}}}
	repo := sdktest.NewFakeRepo("main")
	open := func(id, branch, project string) {
		repo.CRs = append(repo.CRs, sdk.ChangeRequest{ID: id, SourceBranch: branch, State: "open",
			Body: Body(ChangeSet{Project: sdk.Project{ID: project}, Group: "security/g:x"})})
	}
	open("1", "deepward/nuget/src/web/security-x", "nuget:src/Web")
	open("2", "deepward/nuget/legacy/security-x", "nuget:legacy")
	open("3", "deepward/maven/src/web/security-x", "maven:src/Web")
	res, err := e.Publish(context.Background(), repo, plan, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 1 || res.Closed[0].ID != "1" {
		t.Fatalf("closed: %+v", res.Closed)
	}
	if c := repo.Comments["1"]; len(c) != 1 || !strings.Contains(c[0], "project `nuget:src/Web` is now part of project `nuget:.`") {
		t.Errorf("comment: %v", c)
	}
	if mergedInto(nil, "nuget:x") != "" || mergedInto(plan.Result, "nuget:.") != "" || mergedInto(plan.Result, "broken") != "" {
		t.Error("no merge target")
	}
}
