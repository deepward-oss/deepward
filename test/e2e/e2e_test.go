// Package e2e runs the deepward CLI end to end with all built-in plugins,
// a fake build tool and recorded registry/advisory servers.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/cli"
	"github.com/deepward-oss/deepward/pkg/sdk"
	_ "github.com/deepward-oss/deepward/plugins/all"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..")
}

// buildFakeMvn compiles test/e2e/fakemvn into a temp dir named "mvn".
func buildFakeMvn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "mvn"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, "./test/e2e/fakemvn")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake mvn: %v\n%s", err, b)
	}
	return out
}

// osvServer replays the recorded OSV responses.
func osvServer(t *testing.T) *httptest.Server {
	t.Helper()
	data := filepath.Join(repoRoot(t), "plugins", "advisory", "osv", "testdata")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/querybatch":
			// One result per query: only log4j-core has recorded advisories.
			var q struct {
				Queries []struct {
					Package struct{ Name string } `json:"package"`
				} `json:"queries"`
			}
			_ = json.NewDecoder(r.Body).Decode(&q)
			recorded, _ := os.ReadFile(filepath.Join(data, "querybatch.json"))
			var rec struct {
				Results []json.RawMessage `json:"results"`
			}
			_ = json.Unmarshal(recorded, &rec)
			var results []json.RawMessage
			for _, query := range q.Queries {
				if query.Package.Name == "org.apache.logging.log4j:log4j-core" {
					results = append(results, rec.Results[0])
				} else {
					results = append(results, json.RawMessage(`{}`))
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case strings.HasPrefix(r.URL.Path, "/v1/vulns/"):
			b, err := os.ReadFile(filepath.Join(data, strings.TrimPrefix(r.URL.Path, "/v1/vulns/")+".json"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
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
			if info.Name() == "target" {
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

type run struct {
	code   int
	stdout string
	stderr string
}

func deepward(t *testing.T, dir string, env map[string]string, args ...string) run {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Run(context.Background(), cli.Env{
		Args: args, Stdout: &out, Stderr: &errb, Cwd: dir, Registry: sdk.DefaultRegistry(),
		Getenv: func(k string) string { return env[k] }, Environ: func() []string { return []string{"PATH=" + os.Getenv("PATH")} },
		NewTools: cli.DefaultTools, Version: "e2e",
	})
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

// TestScanMavenFixture is task 8.4: scan the Maven fixture with a recorded
// OSV server and check that HTML, JSON and SARIF agree.
func TestScanMavenFixture(t *testing.T) {
	mvn := buildFakeMvn(t)
	osv := osvServer(t)
	defer osv.Close()
	registry := httptest.NewServer(http.NotFoundHandler())
	defer registry.Close()
	dir := t.TempDir()
	copyDir(t, filepath.Join(repoRoot(t), "testdata", "projects", "maven-multi"), dir)
	tree := filepath.Join(repoRoot(t), "plugins", "ecosystem", "maven", "testdata", "tree-multi.json")
	cfg := `failOn: high
output:
  formats: [html, json, sarif]
tools:
  mvn:
    path: ` + filepath.ToSlash(mvn) + `
    env:
      FAKEMVN_TREE: ` + filepath.ToSlash(tree) + `
plugins:
  osv:
    apiUrl: ` + osv.URL + `
  maven:
    repositories:
      - {id: test, url: ` + registry.URL + `}
`
	if err := os.WriteFile(filepath.Join(dir, "deepward.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r := deepward(t, dir, nil, "scan", "--cache-dir", t.TempDir())
	if r.code != cli.ExitGate {
		t.Fatalf("exit %d (want 1: gate)\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, "deepward-out", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var res sdk.ScanResult
	if err := json.Unmarshal(read("deepward-report.json"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Projects) != 1 || res.Projects[0].Status != sdk.ProjectOK || res.Summary.BySeverity[sdk.SeverityCritical] == 0 {
		t.Fatalf("result: projects=%+v summary=%+v warnings=%+v runs=%+v", res.Projects, res.Summary, res.Warnings, res.PluginRuns)
	}
	html := string(read("deepward-report.html"))
	for _, sev := range []sdk.Severity{sdk.SeverityCritical, sdk.SeverityHigh, sdk.SeverityMedium, sdk.SeverityLow} {
		m := regexp.MustCompile(`data-summary="` + string(sev) + `"><span class="num">(\d+)</span>`).FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("HTML lacks the %s card", sev)
		}
		if n, _ := strconv.Atoi(m[1]); n != res.Summary.BySeverity[sev] {
			t.Errorf("%s: HTML %d, JSON %d", sev, n, res.Summary.BySeverity[sev])
		}
	}
	var sarif struct {
		Runs []struct {
			Results []struct {
				Properties struct {
					Deepward struct {
						Severity string `json:"severity"`
					} `json:"deepward"`
				} `json:"properties"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(read("deepward-report.sarif"), &sarif); err != nil {
		t.Fatal(err)
	}
	sarifCounts := map[string]int{}
	for _, r := range sarif.Runs[0].Results {
		sarifCounts[r.Properties.Deepward.Severity]++
	}
	for sev, n := range res.Summary.BySeverity {
		if sarifCounts[string(sev)] != n {
			t.Errorf("%s: SARIF %d, JSON %d", sev, sarifCounts[string(sev)], n)
		}
	}
	if !strings.Contains(r.stdout, "Quality gate (high): FAILED") || !strings.Contains(r.stdout, "deepward-out/deepward-report.html") {
		t.Errorf("summary:\n%s", r.stdout)
	}
	if !strings.Contains(html, "Vulnerability data: <strong>osv</strong> (CC-BY-4.0") {
		t.Error("data attribution footer")
	}
}
