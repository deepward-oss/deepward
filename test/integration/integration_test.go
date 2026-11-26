//go:build integration

// Package integration scans the sample projects with the real build tools
// (Maven, sbt, .NET SDK) running in Docker through deepward's container
// resolver, and the live OSV.dev API. Run with: make integration.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/internal/cli"
	"github.com/deepward-oss/deepward/pkg/sdk"
	_ "github.com/deepward-oss/deepward/plugins/all"
)

type tc struct {
	name, fixture, tool, image, executable string
	extra                                  string
	wantRule, wantPackage                  string
	// fixWant lists text expected in the output of a dry-run "deepward fix".
	// Fixed versions move as advisories are published, so only the change
	// and the regenerated lockfile are checked.
	fixWant []string
}

var cases = []tc{
	{name: "maven", fixture: "maven-multi", tool: "mvn", image: "maven:3.9.9-eclipse-temurin-17",
		extra: "    args: [\"-Dmaven.repo.local={{home}}/.m2/repository\"]\n", wantRule: "CVE-2021-44228", wantPackage: "log4j-core"},
	{name: "sbt", fixture: "sbt-app", tool: "sbt", image: "sbtscala/scala-sbt:eclipse-temurin-alpine-21.0.13_9_1.13.1_3.9.0",
		wantRule: "CVE-2021-44228", wantPackage: "log4j-core"},
	// The .NET SDK fails its first-run migrations for UIDs it does not know,
	// so it runs as the image's user.
	{name: "nuget", fixture: "nuget-app", tool: "dotnet", image: "mcr.microsoft.com/dotnet/sdk:8.0",
		extra: "    containerUser: image\n", wantRule: "CVE-2024-21907", wantPackage: "newtonsoft.json"},
	// Lockfile ecosystems scan without their package manager; npm runs for fixes.
	{name: "npm", fixture: "npm-app", tool: "npm", image: "node:22-bookworm",
		extra: "      npm_config_cache: {{home}}/.npm\n", wantRule: "CVE-2021-23337", wantPackage: "lodash",
		fixWant: []string{"fix vulnerabilities: lodash 4.17.20 → ", "+++ b/package.json", "+++ b/package-lock.json"}},
	{name: "pnpm", fixture: "pnpm-app", tool: "pnpm", image: "node:22-bookworm", wantRule: "CVE-2021-23337", wantPackage: "lodash"},
	{name: "yarn", fixture: "yarn-app", tool: "yarn", image: "node:22-bookworm", wantRule: "CVE-2021-23337", wantPackage: "lodash"},
	{name: "go", fixture: "go-app", tool: "go", image: "golang:1.26-bookworm",
		extra: "      GOPATH: {{home}}/go\n      GOCACHE: {{home}}/.cache/go-build\n", wantRule: "CVE-2022-32149", wantPackage: "golang.org/x/text",
		fixWant: []string{"fix vulnerabilities: golang.org/x/text v0.3.7 → ", "+++ b/go.mod", "+++ b/go.sum"}},
	{name: "cargo", fixture: "cargo-app", tool: "cargo", image: "rust:1-bookworm",
		extra: "      CARGO_HOME: {{home}}/.cargo\n", wantRule: "RUSTSEC-2021-0003", wantPackage: "smallvec",
		fixWant: []string{"fix vulnerabilities: smallvec 1.6.0 → ", "+++ b/app/Cargo.toml", "+++ b/Cargo.lock"}},
}

func repoRoot(t *testing.T) string {
	wd, _ := os.Getwd()
	if r := os.Getenv("DEEPWARD_REPO_ROOT"); r != "" {
		return r
	}
	return filepath.Join(wd, "..", "..")
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			if info.Name() == "target" || info.Name() == "obj" || info.Name() == "bin" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRealBuildTools(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available")
	}
	cacheRoot := os.Getenv("DEEPWARD_IT_CACHE")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "deepward-it")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A stable directory keeps the tools' caches (under .home) between runs.
			ws := filepath.Join(cacheRoot, c.name)
			home := filepath.Join(ws, ".home")
			_ = os.MkdirAll(filepath.Join(home, "tmp"), 0o755)
			entries, _ := os.ReadDir(ws)
			for _, e := range entries {
				if e.Name() != ".home" {
					_ = os.RemoveAll(filepath.Join(ws, e.Name()))
				}
			}
			copyDir(t, filepath.Join(repoRoot(t), "testdata", "projects", c.fixture), ws)
			cfg := "output:\n  formats: [json]\nexclude: [.home]\nplugins:\n  outdated-dependencies:\n    enabled: false\ntools:\n  " + c.tool + ":\n" +
				"    resolvers: [container]\n    image: " + c.image + "\n    timeout: 20m\n" +
				"    env:\n      HOME: " + home + "\n      DOTNET_CLI_HOME: " + home + "\n      NUGET_PACKAGES: " + home + "/.nuget/packages\n" +
				"      JAVA_TOOL_OPTIONS: -Duser.home=" + home + "\n      COURSIER_CACHE: " + home + "/.cache/coursier\n      TMPDIR: " + home + "/tmp\n" +
				strings.ReplaceAll(c.extra, "{{home}}", home)
			if err := os.WriteFile(filepath.Join(ws, "deepward.yaml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			code := cli.Run(ctx, cli.Env{Args: []string{"scan", "--cache-dir", filepath.Join(cacheRoot, "cache")}, Stdout: &out, Stderr: &errb,
				Cwd: ws, Registry: sdk.DefaultRegistry(), Getenv: os.Getenv, Environ: os.Environ, NewTools: cli.DefaultTools})
			data, err := os.ReadFile(filepath.Join(ws, "deepward-out", "deepward-report.json"))
			if err != nil {
				t.Fatalf("exit %d, no report: %v\n%s\n%s", code, err, out.String(), errb.String())
			}
			var res sdk.ScanResult
			if err := json.Unmarshal(data, &res); err != nil {
				t.Fatal(err)
			}
			for _, p := range res.Projects {
				if p.Status != sdk.ProjectOK {
					t.Fatalf("project %s %s: %s\n%s", p.ID, p.Status, p.Reason, strings.Join(p.Output, "\n"))
				}
			}
			found := false
			for _, f := range res.Findings {
				ids := append([]string{f.Rule}, f.Aliases...)
				for _, id := range ids {
					if id == c.wantRule && strings.Contains(strings.ToLower(f.PrimaryPackage().PURL), c.wantPackage) {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("expected %s on %s; findings: %d, summary %+v", c.wantRule, c.wantPackage, len(res.Findings), res.Summary.BySeverity)
			}
			if len(c.fixWant) == 0 {
				return
			}
			out.Reset()
			errb.Reset()
			code = cli.Run(ctx, cli.Env{Args: []string{"fix", "--cache-dir", filepath.Join(cacheRoot, "cache")}, Stdout: &out, Stderr: &errb,
				Cwd: ws, Registry: sdk.DefaultRegistry(), Getenv: os.Getenv, Environ: os.Environ, NewTools: cli.DefaultTools})
			for _, want := range c.fixWant {
				if !strings.Contains(out.String(), want) {
					t.Errorf("fix (exit %d) does not contain %q:\n%s\n%s", code, want, out.String(), errb.String())
				}
			}
		})
	}
}
