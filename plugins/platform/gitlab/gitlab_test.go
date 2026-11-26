package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// fakeGitLab is a stateful GitLab API for one project ("team/billing").
type fakeGitLab struct {
	mu       sync.Mutex
	files    map[string]map[string]string // branch -> path -> content
	commits  map[string][]map[string]string
	mrs      []map[string]any
	notes    map[string][]string
	calls    []string
	failNext string
}

func newFake() *fakeGitLab {
	return &fakeGitLab{files: map[string]map[string]string{"main": {"pom.xml": "<project/>"}}, commits: map[string][]map[string]string{}, notes: map[string][]string{}}
}

const project = "/api/v4/projects/team%2Fbilling"

func (f *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.EscapedPath()
	f.calls = append(f.calls, r.Method+" "+strings.TrimPrefix(path, project))
	if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.failNext != "" && strings.Contains(path, f.failNext) {
		f.failNext = ""
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	rest := strings.TrimPrefix(path, project)
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case rest == "" && r.Method == http.MethodGet:
		write(map[string]any{"default_branch": "main"})
	case strings.HasPrefix(rest, "/repository/files/") && strings.HasSuffix(rest, "/raw"):
		p, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(rest, "/repository/files/"), "/raw"))
		content, ok := f.files[r.URL.Query().Get("ref")][p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(content))
	case rest == "/merge_requests" && r.Method == http.MethodGet:
		var open []map[string]any
		for _, m := range f.mrs {
			if m["state"] == "opened" {
				open = append(open, m)
			}
		}
		if r.URL.Query().Get("page") == "1" && len(open) > 1 {
			w.Header().Set("X-Next-Page", "2")
			write(open[:1])
			return
		}
		if r.URL.Query().Get("page") == "2" {
			write(open[1:])
			return
		}
		write(open)
	case rest == "/repository/compare":
		to := r.URL.Query().Get("to")
		cs, ok := f.commits[to]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(map[string]any{"commits": cs})
	case rest == "/repository/commits" && r.Method == http.MethodPost:
		branch, start := body["branch"].(string), body["start_branch"].(string)
		if _, exists := f.files[branch]; exists && body["force"] != true {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		files := map[string]string{}
		for k, v := range f.files[start] {
			files[k] = v
		}
		for _, a := range body["actions"].([]any) {
			act := a.(map[string]any)
			p := act["file_path"].(string)
			_, existed := f.files[start][p]
			if (act["action"] == "update") != existed && act["action"] != "delete" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("wrong action for " + p))
				return
			}
			if act["action"] == "delete" {
				delete(files, p)
				continue
			}
			b, _ := base64.StdEncoding.DecodeString(act["content"].(string))
			files[p] = string(b)
		}
		f.files[branch] = files
		sha := fmt.Sprintf("sha%d", len(f.calls))
		f.commits[branch] = []map[string]string{{"id": sha, "author_name": body["author_name"].(string), "author_email": body["author_email"].(string)}}
		write(map[string]any{"id": sha})
	case rest == "/merge_requests" && r.Method == http.MethodPost:
		iid := len(f.mrs) + 1
		labels := strings.Split(body["labels"].(string), ",")
		m := map[string]any{"iid": iid, "web_url": fmt.Sprintf("https://gitlab.example/team/billing/-/merge_requests/%d", iid),
			"title": body["title"], "description": body["description"], "source_branch": body["source_branch"],
			"target_branch": body["target_branch"], "labels": labels, "state": "opened"}
		f.mrs = append(f.mrs, m)
		write(m)
	case strings.HasPrefix(rest, "/merge_requests/") && strings.HasSuffix(rest, "/notes"):
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/merge_requests/"), "/notes")
		f.notes[id] = append(f.notes[id], body["body"].(string))
		write(map[string]any{"id": 1})
	case strings.HasPrefix(rest, "/merge_requests/") && r.Method == http.MethodPut:
		iid, _ := strconv.Atoi(strings.TrimPrefix(rest, "/merge_requests/"))
		m := f.mrs[iid-1]
		if body["state_event"] == "close" {
			m["state"] = "closed"
		} else {
			m["title"], m["description"], m["labels"] = body["title"], body["description"], strings.Split(body["labels"].(string), ",")
		}
		write(m)
	default:
		http.NotFound(w, r)
	}
}

func connect(t *testing.T, srv *httptest.Server) sdk.PlatformClient {
	t.Helper()
	p := &Platform{HTTP: srv.Client()}
	return sdktest.TestPlatform(t, p, sdk.PlatformConfig{APIURL: srv.URL + "/api/v4", Repo: "team/billing", Token: "glpat-test"})
}

func TestPublishFlow(t *testing.T) {
	fake := newFake()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := connect(t, srv)
	ctx := context.Background()
	if b, _ := client.DefaultBranch(ctx); b != "main" {
		t.Fatal("default branch")
	}
	e, err := engine.New(ctx, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cs := engine.ChangeSet{Project: sdk.Project{ID: "maven:."}, Group: "security/g:lib", Branch: "deepward/maven/security-lib",
		Title: "Deepward: fix vulnerabilities: g:lib 1.0 → 2.0", Security: true, MaxSeverity: sdk.SeverityCritical, Labels: []string{"dependencies", "security"},
		Items: []engine.FixItem{{Package: "pkg:maven/g/lib", From: "1.0", To: "2.0", Kind: sdk.KindVulnerability}},
		Files: []sdk.FileChange{{Path: "pom.xml", Content: []byte("<project><v>2.0</v></project>")}, {Path: "new.txt", Content: []byte("x")}}}
	plan := &engine.FixPlan{ChangeSets: []engine.ChangeSet{cs}, Result: &sdk.ScanResult{Projects: []sdk.ProjectResult{{Project: sdk.Project{ID: "maven:."}, Status: sdk.ProjectOK}}}}
	res, err := e.Publish(ctx, client, plan, "main")
	if err != nil || len(res.Created) != 1 || res.Created[0].URL == "" || res.Created[0].Labels[1] != "security" {
		t.Fatalf("create: %+v %v", res, err)
	}
	if fake.files["deepward/maven/security-lib"]["pom.xml"] != "<project><v>2.0</v></project>" || fake.commits["deepward/maven/security-lib"][0]["author_email"] != sdk.BotAuthor.Email {
		t.Errorf("commit: %+v", fake.files)
	}
	content, ok, _ := client.ReadFile(ctx, "deepward/maven/security-lib", "new.txt")
	if !ok || string(content) != "x" {
		t.Error("read file")
	}
	if _, ok, _ := client.ReadFile(ctx, "main", "missing.txt"); ok {
		t.Error("missing file")
	}
	// Second run updates the same MR.
	res, _ = e.Publish(ctx, client, plan, "main")
	if len(res.Updated) != 1 || len(fake.mrs) != 1 {
		t.Errorf("update: %+v", res)
	}
	// A human commit pauses it.
	fake.commits["deepward/maven/security-lib"] = append(fake.commits["deepward/maven/security-lib"], map[string]string{"id": "h", "author_name": "Dev", "author_email": "dev@acme"})
	res, _ = e.Publish(ctx, client, plan, "main")
	if len(res.Paused) != 1 || len(fake.notes["1"]) != 1 {
		t.Errorf("pause: %+v notes=%v", res, fake.notes)
	}
	// The update is no longer needed: the MR is closed with a comment.
	empty := &engine.FixPlan{Result: plan.Result}
	res, _ = e.Publish(ctx, client, empty, "main")
	if len(res.Closed) != 1 || fake.mrs[0]["state"] != "closed" || len(fake.notes["1"]) != 2 {
		t.Errorf("close: %+v", res)
	}
}

func TestPaginationAndErrors(t *testing.T) {
	fake := newFake()
	fake.mrs = []map[string]any{
		{"iid": 1, "source_branch": "deepward/a", "state": "opened"},
		{"iid": 2, "source_branch": "other/b", "state": "opened"},
		{"iid": 3, "source_branch": "deepward/c", "state": "opened"},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	client := connect(t, srv)
	ctx := context.Background()
	crs, err := client.ListChangeRequests(ctx, "deepward/")
	if err != nil || len(crs) != 2 || crs[1].ID != "3" {
		t.Errorf("pagination: %+v %v", crs, err)
	}
	if commits, err := client.BranchCommits(ctx, "nope", "main"); err != nil || commits != nil {
		t.Error("missing branch has no commits")
	}
	if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: "main", BaseBranch: "main"}); err == nil {
		t.Error("existing branch without force")
	}
	if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: "x", BaseBranch: "main", Files: []sdk.FileChange{{Path: "pom.xml", Delete: true}}}); err != nil {
		t.Errorf("delete: %v", err)
	}
	for _, fail := range []string{"/merge_requests", "/repository/compare", "/repository/files", "/notes"} {
		fake.failNext = fail
		var err error
		switch fail {
		case "/merge_requests":
			_, err = client.ListChangeRequests(ctx, "")
		case "/repository/compare":
			_, err = client.BranchCommits(ctx, "x", "main")
		case "/repository/files":
			_, err = client.Commit(ctx, sdk.CommitRequest{Branch: "y", BaseBranch: "main", Files: []sdk.FileChange{{Path: "a"}}})
		case "/notes":
			err = client.Comment(ctx, "1", "x")
		}
		if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("%s: %v", fail, err)
		}
	}
	fake.failNext = "projects"
	if _, err := client.DefaultBranch(ctx); err == nil {
		t.Error("default branch failure")
	}
	for _, f := range []func() error{
		func() error {
			_, err := client.CreateChangeRequest(ctx, sdk.ChangeRequestSpec{})
			fake.failNext = "/merge_requests/1"
			return err
		},
		func() error { _, err := client.UpdateChangeRequest(ctx, "1", sdk.ChangeRequestSpec{}); return err },
	} {
		fake.failNext = "/merge_requests"
		if err := f(); err == nil {
			t.Error("create/update failure")
		}
	}
	fake.failNext = "/merge_requests/1"
	if err := client.CloseChangeRequest(ctx, "1"); err == nil {
		t.Error("close failure")
	}
	bad := &Client{api: "http://[::1", project: "x", token: "t", http: http.DefaultClient}
	if _, err := bad.DefaultBranch(ctx); err == nil {
		t.Error("bad url")
	}
	dead := &Client{api: "http://127.0.0.1:1", project: "x", token: "t", http: http.DefaultClient}
	if _, err := dead.DefaultBranch(ctx); err == nil {
		t.Error("unreachable")
	}
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer garbage.Close()
	g := &Client{api: garbage.URL, project: "x", token: "t", http: garbage.Client()}
	if _, err := g.DefaultBranch(ctx); err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Errorf("garbage: %v", err)
	}
	if _, err := g.do(ctx, http.MethodPost, "/x", map[string]any{"bad": func() {}}, nil); err == nil {
		t.Error("unmarshalable body")
	}
}

func TestConnectAndDetect(t *testing.T) {
	p := &Platform{}
	if _, err := p.Connect(context.Background(), sdk.PlatformConfig{Repo: "a/b"}); err == nil {
		t.Error("token required")
	}
	if _, err := p.Connect(context.Background(), sdk.PlatformConfig{Token: "t"}); err == nil {
		t.Error("repo required")
	}
	c, _ := p.Connect(context.Background(), sdk.PlatformConfig{Token: "t", Repo: "team/billing", RepoURL: "https://git.acme.local/team/billing.git"})
	if c.(*Client).api != "https://git.acme.local/api/v4" || c.(*Client).project != "team%2Fbilling" {
		t.Errorf("self-managed API URL: %+v", c)
	}
	for in, want := range map[string]string{"": DefaultAPI, "git@gitlab.acme:team/x.git": "https://gitlab.acme/api/v4", "::bad": DefaultAPI} {
		if got := APIURL(in); got != want {
			t.Errorf("APIURL(%q) = %s", in, got)
		}
	}
	if !p.Detect("https://gitlab.com/a/b") || !p.Detect("git@gitlab.acme.local:a/b.git") || p.Detect("https://github.com/a/b") || p.Detect("::") {
		t.Error("detect")
	}
}
