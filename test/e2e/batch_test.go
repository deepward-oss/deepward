package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const manyLibs = 10

// manyFixture writes a Maven project with ten direct dependencies at 1.0.0
// and the dependency tree fakemvn serves for it.
func manyFixture(t *testing.T, dir string) string {
	t.Helper()
	var deps strings.Builder
	type node struct {
		GroupID    string  `json:"groupId"`
		ArtifactID string  `json:"artifactId"`
		Version    string  `json:"version"`
		Type       string  `json:"type"`
		Scope      string  `json:"scope"`
		Classifier string  `json:"classifier"`
		Optional   string  `json:"optional"`
		Children   []*node `json:"children,omitempty"`
	}
	root := &node{GroupID: "com.example", ArtifactID: "app", Version: "1.0.0", Type: "jar", Optional: "false"}
	for i := 1; i <= manyLibs; i++ {
		lib := fmt.Sprintf("lib%02d", i)
		fmt.Fprintf(&deps, "    <dependency>\n      <groupId>com.example.libs</groupId>\n      <artifactId>%s</artifactId>\n      <version>1.0.0</version>\n    </dependency>\n", lib)
		root.Children = append(root.Children, &node{GroupID: "com.example.libs", ArtifactID: lib, Version: "1.0.0", Type: "jar", Scope: "compile", Optional: "false"})
	}
	pom := "<project>\n  <modelVersion>4.0.0</modelVersion>\n  <groupId>com.example</groupId>\n  <artifactId>app</artifactId>\n  <version>1.0.0</version>\n" +
		"  <dependencies>\n" + deps.String() + "  </dependencies>\n</project>\n"
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pom), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, _ := json.MarshalIndent(root, "", "  ")
	path := filepath.Join(t.TempDir(), "tree.json")
	if err := os.WriteFile(path, tree, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// manyAdvisories serves one high advisory per library, fixed in 1.0.1.
func manyAdvisories(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/querybatch":
			var q struct {
				Queries []struct {
					Package struct{ Name string } `json:"package"`
				} `json:"queries"`
			}
			_ = json.NewDecoder(r.Body).Decode(&q)
			var results []any
			for _, query := range q.Queries {
				lib, ok := strings.CutPrefix(query.Package.Name, "com.example.libs:")
				if !ok {
					results = append(results, map[string]any{})
					continue
				}
				results = append(results, map[string]any{"vulns": []any{map[string]any{"id": "CVE-E2E-" + lib, "modified": "2026-01-01T00:00:00Z"}}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case strings.HasPrefix(r.URL.Path, "/v1/vulns/CVE-E2E-"):
			lib := strings.TrimPrefix(r.URL.Path, "/v1/vulns/CVE-E2E-")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "CVE-E2E-" + lib, "summary": "issue in " + lib, "modified": "2026-01-01T00:00:00Z",
				"database_specific": map[string]any{"severity": "HIGH"},
				"affected": []any{map[string]any{
					"package": map[string]any{"ecosystem": "Maven", "name": "com.example.libs:" + lib},
					"ranges":  []any{map[string]any{"type": "ECOSYSTEM", "events": []any{map[string]any{"introduced": "0"}, map[string]any{"fixed": "1.0.1"}}}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestBatchVerificationSavesBuilds fixes ten independent vulnerable
// dependencies with both verification modes and compares the dependency
// tree runs and the time they took.
func TestBatchVerificationSavesBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: every fake mvn run sleeps")
	}
	mvn := buildFakeMvn(t)
	osv := manyAdvisories(t)
	defer osv.Close()
	registry := httptest.NewServer(http.NotFoundHandler())
	defer registry.Close()
	type outcome struct {
		trees    int
		duration time.Duration
		stdout   string
	}
	fix := func(mode string) outcome {
		dir := t.TempDir()
		tree := manyFixture(t, dir)
		counter := filepath.Join(t.TempDir(), "calls.txt")
		cfg := `tools:
  mvn:
    path: ` + filepath.ToSlash(mvn) + `
    env:
      FAKEMVN_TREE: ` + filepath.ToSlash(tree) + `
      FAKEMVN_COUNTER: ` + filepath.ToSlash(counter) + `
      FAKEMVN_DELAY: 400ms
plugins:
  osv:
    apiUrl: ` + osv.URL + `
  maven:
    repositories:
      - {id: test, url: ` + registry.URL + `}
  outdated-dependencies:
    enabled: false
fix:
  verify: ` + mode + `
`
		if err := os.WriteFile(filepath.Join(dir, "deepward.yaml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		r := deepward(t, dir, nil, "fix", "--cache-dir", t.TempDir())
		took := time.Since(start)
		if r.code != 0 {
			t.Fatalf("%s: fix exit %d\n%s\n%s", mode, r.code, r.stdout, r.stderr)
		}
		calls, _ := os.ReadFile(counter)
		return outcome{trees: strings.Count(string(calls), "tree\n"), duration: took, stdout: r.stdout}
	}
	each := fix("each")
	batch := fix("batch")
	t.Logf("mode | mvn runs | duration\neach | %d | %s\nbatch | %d | %s", each.trees, each.duration, batch.trees, batch.duration)
	for name, o := range map[string]outcome{"each": each, "batch": batch} {
		if n := strings.Count(o.stdout, "+      <version>1.0.1</version>"); n != manyLibs {
			t.Errorf("%s: %d of %d libraries fixed:\n%s", name, n, manyLibs, o.stdout)
		}
	}
	if each.trees < 3*batch.trees {
		t.Errorf("batch must need at least three times fewer mvn runs: each %d, batch %d", each.trees, batch.trees)
	}
	if batch.duration*2 >= each.duration {
		t.Errorf("batch must take less than half the time: each %s, batch %s", each.duration, batch.duration)
	}
}
