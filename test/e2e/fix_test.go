package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordingGitLab is a minimal GitLab that records every API call.
type recordingGitLab struct {
	mu    sync.Mutex
	calls []string
	body  map[string]any
}

func (g *recordingGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4/projects/team%2Fbilling")
	g.calls = append(g.calls, r.Method+" "+path)
	if r.Header.Get("PRIVATE-TOKEN") != "glpat-e2e" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == http.MethodGet && path == "/merge_requests":
		_, _ = w.Write([]byte(`[]`))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repository/files/"):
		_, _ = w.Write([]byte("<project/>"))
	case r.Method == http.MethodPost && path == "/repository/commits":
		g.body = body
		_, _ = w.Write([]byte(`{"id":"c1"}`))
	case r.Method == http.MethodPost && path == "/merge_requests":
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 1, "web_url": "https://gitlab.example/team/billing/-/merge_requests/1",
			"title": body["title"], "source_branch": body["source_branch"], "target_branch": body["target_branch"], "state": "opened"})
	default:
		http.NotFound(w, r)
	}
}

// TestFixPublishEndToEnd is task 17.2: the real binary logic with all
// built-in plugins scans the Maven fixture, verifies the log4j fix by
// rebuilding, and publishes exactly one merge request.
func TestFixPublishEndToEnd(t *testing.T) {
	mvn := buildFakeMvn(t)
	osv := osvServer(t)
	defer osv.Close()
	registry := httptest.NewServer(http.NotFoundHandler())
	defer registry.Close()
	gitlab := &recordingGitLab{}
	gl := httptest.NewServer(gitlab)
	defer gl.Close()
	dir := t.TempDir()
	copyDir(t, filepath.Join(repoRoot(t), "testdata", "projects", "maven-multi"), dir)
	tree := filepath.Join(repoRoot(t), "plugins", "ecosystem", "maven", "testdata", "tree-multi.json")
	cfg := `failOn: high
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
  outdated-dependencies:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(dir, "deepward.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(dir, "pom.xml"))
	env := map[string]string{
		"DEEPWARD_GITLAB_TOKEN": "glpat-e2e", "GITLAB_CI": "true", "CI_PROJECT_URL": "https://gitlab.example/team/billing",
		"CI_PROJECT_PATH": "team/billing", "CI_DEFAULT_BRANCH": "main", "CI_API_V4_URL": gl.URL + "/api/v4", "CI_COMMIT_SHA": "0a1b2c3",
	}
	scan := deepward(t, dir, env, "scan", "--format", "html,json,sarif", "--cache-dir", t.TempDir())
	if scan.code != 1 {
		t.Fatalf("scan exit %d (want 1, gate)\n%s\n%s", scan.code, scan.stdout, scan.stderr)
	}
	for _, f := range []string{"deepward-report.html", "deepward-report.json", "deepward-report.sarif"} {
		if _, err := os.Stat(filepath.Join(dir, "deepward-out", f)); err != nil {
			t.Errorf("missing report %s", f)
		}
	}
	// The fixed version comes from the (recorded) advisory data.
	var report struct {
		Findings []struct {
			Properties map[string]any `json:"properties"`
		} `json:"findings"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, "deepward-out", "deepward-report.json"))
	_ = json.Unmarshal(data, &report)
	fixed := ""
	for _, f := range report.Findings {
		if v, ok := f.Properties["fixedIn"].(string); ok {
			fixed = v
		}
	}
	if fixed == "" || fixed == "2.14.1" {
		t.Fatalf("fixedIn: %q", fixed)
	}
	dry := deepward(t, dir, env, "fix", "--cache-dir", t.TempDir())
	if dry.code != 0 || !strings.Contains(dry.stdout, "-    <log4j.version>2.14.1</log4j.version>") ||
		!strings.Contains(dry.stdout, "+    <log4j.version>"+fixed+"</log4j.version>") || len(gitlab.calls) != 0 {
		t.Fatalf("dry run: exit %d calls=%v\n%s\n%s", dry.code, gitlab.calls, dry.stdout, dry.stderr)
	}
	run := deepward(t, dir, env, "fix", "--publish", "--cache-dir", t.TempDir())
	if run.code != 0 {
		t.Fatalf("fix --publish exit %d\n%s\n%s", run.code, run.stdout, run.stderr)
	}
	want := []string{
		"GET /merge_requests",
		"GET /repository/files/pom.xml/raw",
		"POST /repository/commits",
		"POST /merge_requests",
	}
	if strings.Join(gitlab.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("API calls:\n%s\nwant:\n%s", strings.Join(gitlab.calls, "\n"), strings.Join(want, "\n"))
	}
	if gitlab.body["branch"] != "deepward/maven/security-log4j-core" || gitlab.body["start_branch"] != "main" || gitlab.body["force"] != true {
		t.Errorf("commit request: %+v", gitlab.body)
	}
	if !strings.Contains(run.stdout, "created: https://gitlab.example/team/billing/-/merge_requests/1") {
		t.Errorf("summary:\n%s", run.stdout)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "pom.xml"))
	if string(after) != string(original) {
		t.Error("fix must not modify the working copy")
	}
}
