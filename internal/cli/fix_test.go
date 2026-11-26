package cli

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// fixPlugins returns an ecosystem that edits versions.txt and an analyzer
// that reports "lib" below 2.0 with a remediation to 2.0.
func fixPlugins(platform *sdktest.FakePlatform) []sdk.Plugin {
	eco := &sdktest.FakeEcosystem{ID: "maven", Marker: "versions.txt", RealizeFunc: func(_ context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
		data, _ := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
		i := strings.Index(string(data), "lib=1.0")
		return sdk.RealizeResult{Edits: []sdk.TextEdit{{Path: "app/versions.txt", Start: i + 4, End: i + 7, Replacement: "2.0"}}}, nil
	}}
	analyzer := &sdktest.FakeAnalyzer{ID: "vulns", Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		data, _ := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
		if !strings.Contains(string(data), "lib=1.0") {
			return nil, nil
		}
		return []sdk.Finding{{Rule: "CVE-1", Kind: sdk.KindVulnerability, Severity: sdk.SeverityCritical, Message: "lib is vulnerable",
			Locations:    []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/g/lib@1.0", Direct: true}}},
			Remediations: []sdk.Remediation{{Description: "upgrade", Group: "security/g:lib", Package: &sdk.PackageChange{PURL: "pkg:maven/g/lib", From: "1.0", To: "2.0"}}}}}, nil
	}}
	return []sdk.Plugin{eco, analyzer, platform}
}

func fixHarness(t *testing.T) (*harness, *sdktest.FakePlatform) {
	platform := &sdktest.FakePlatform{ID: "gitlab", Domain: "git.acme.local"}
	h := newHarness(t, fixPlugins(platform)...)
	h.write("app/versions.txt", "lib=1.0\n")
	return h, platform
}

func TestFixDryRun(t *testing.T) {
	h, platform := fixHarness(t)
	if code := h.run("fix"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"### Deepward: fix vulnerabilities: g:lib 1.0 → 2.0 in app", "branch: deepward/maven/app/security-lib", "-lib=1.0", "+lib=2.0", "Nothing was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if platform.Repo != nil {
		t.Error("dry run must not connect to the platform")
	}
	if code := h.run("fix", "--quiet"); code != ExitOK || strings.Contains(h.stdout.String(), "Nothing was written") {
		t.Error("quiet dry run prints only diffs")
	}
}

func TestFixPublish(t *testing.T) {
	h, platform := fixHarness(t)
	h.env["DEEPWARD_GITLAB_TOKEN"] = "glpat-x"
	h.env["GITLAB_CI"] = "true"
	h.env["CI_PROJECT_URL"] = "https://git.acme.local/team/billing"
	h.env["CI_PROJECT_PATH"] = "team/billing"
	h.env["CI_DEFAULT_BRANCH"] = "main"
	if code := h.run("fix", "--publish"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "created: https://fake/cr/") || len(platform.Repo.Writes) != 2 {
		t.Errorf("publish: %s writes=%v", h.stdout.String(), platform.Repo.Writes)
	}
	if code := h.run("fix", "--publish"); code != ExitOK || !strings.Contains(h.stdout.String(), "updated:") {
		t.Errorf("second publish updates: %s", h.stdout.String())
	}
}

func TestFixPlatformSelection(t *testing.T) {
	h, _ := fixHarness(t)
	h.env["DEEPWARD_GITHUB_TOKEN"] = "ghp"
	if code := h.run("fix", "--publish", "--repo-url", "https://elsewhere/x/y"); code != ExitUsage || !strings.Contains(h.stderr.String(), "cannot detect the hosting platform") {
		t.Errorf("undetectable: %d %s", code, h.stderr.String())
	}
	if code := h.run("fix", "--publish", "--platform", "bitbucket"); code != ExitUsage {
		t.Errorf("unknown platform: %d", code)
	}
	if code := h.run("fix", "--publish", "--platform", "gitlab"); code != ExitUsage || !strings.Contains(h.stderr.String(), "cannot determine the repository") {
		t.Errorf("no repo: %d %s", code, h.stderr.String())
	}
	if code := h.run("fix", "--publish", "--platform", "gitlab", "--repo", "g/p"); code != ExitUsage || !strings.Contains(h.stderr.String(), "DEEPWARD_GITLAB_TOKEN") {
		t.Errorf("token of the other platform: %d %s", code, h.stderr.String())
	}
	h.env["DEEPWARD_GITLAB_TOKEN"] = "glpat"
	if code := h.run("fix", "--publish", "--repo-url", "git@git.acme.local:team/app.git", "--target-branch", "develop", "--api-url", "https://git.acme.local/api/v4"); code != ExitOK {
		t.Errorf("detected from ssh URL: %d %s", code, h.stderr.String())
	}
	h2, platform := fixHarness(t)
	h2.env["DEEPWARD_GITLAB_TOKEN"] = "glpat"
	h2.write("deepward.yaml", "platform:\n  type: gitlab\n  repo: team/app\nfix:\n  targetBranch: release\n")
	if code := h2.run("fix", "--publish"); code != ExitOK || platform.Repo.CRs[0].TargetBranch != "release" {
		t.Errorf("config platform: %d %s %+v", code, h2.stderr.String(), platform.Repo)
	}
	h3, p3 := fixHarness(t)
	h3.env["DEEPWARD_GITLAB_TOKEN"] = "glpat"
	if code := h3.run("fix", "--publish", "--platform", "gitlab", "--repo", "a/b"); code != ExitOK || p3.Repo.CRs[0].TargetBranch != "main" {
		t.Errorf("default branch from the platform: %d %s", code, h3.stderr.String())
	}
}

func TestRepoFromURL(t *testing.T) {
	cases := map[string]string{"https://git.acme.local/team/sub/app.git": "team/sub/app", "git@github.com:acme/app.git": "acme/app", "": "", "http://[::1": ""}
	for in, want := range cases {
		if got := repoFromURL(in); got != want {
			t.Errorf("repoFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
