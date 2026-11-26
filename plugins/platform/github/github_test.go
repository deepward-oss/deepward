package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// fakeGitHub is a stateful GitHub API for acme/app.
type fakeGitHub struct {
	mu       sync.Mutex
	refs     map[string]string            // branch -> commit sha
	commits  map[string]map[string]any    // sha -> {tree, parents, author}
	trees    map[string]map[string]string // tree sha -> path -> blob sha
	blobs    map[string]string
	pulls    []map[string]any
	comments map[string][]string
	n        int
	failNext string
	srvURL   string
}

func newFake() *fakeGitHub {
	f := &fakeGitHub{refs: map[string]string{"main": "c0"}, commits: map[string]map[string]any{"c0": {"tree": "t0"}},
		trees: map[string]map[string]string{"t0": {"pom.xml": "b0"}}, blobs: map[string]string{"b0": "<project/>"}, comments: map[string][]string{}}
	return f
}

const repo = "/repos/acme/app"

func (f *fakeGitHub) id(prefix string) string { f.n++; return fmt.Sprintf("%s%d", prefix, f.n) }

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer ghp_test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	if f.failNext != "" && strings.Contains(path, f.failNext) {
		f.failNext = ""
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	rest := strings.TrimPrefix(path, repo)
	switch {
	case rest == "" && r.Method == http.MethodGet:
		write(map[string]any{"default_branch": "main"})
	case strings.HasPrefix(rest, "/contents/"):
		sha, ok := f.refs[r.URL.Query().Get("ref")]
		blob, ok2 := f.trees[f.commits[sha]["tree"].(string)][strings.TrimPrefix(rest, "/contents/")]
		if !ok || !ok2 || r.Header.Get("Accept") != "application/vnd.github.raw" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(f.blobs[blob]))
	case rest == "/pulls" && r.Method == http.MethodGet:
		var open []map[string]any
		for _, p := range f.pulls {
			if p["state"] == "open" {
				open = append(open, p)
			}
		}
		if r.URL.Query().Get("page") == "" && len(open) > 1 {
			w.Header().Set("Link", `<`+f.srvURL+repo+`/pulls?state=open&per_page=100&page=2>; rel="next"`)
			write(open[:1])
			return
		}
		if r.URL.Query().Get("page") == "2" {
			write(open[1:])
			return
		}
		write(open)
	case strings.HasPrefix(rest, "/compare/"):
		_, head, _ := strings.Cut(strings.TrimPrefix(rest, "/compare/"), "...")
		sha, ok := f.refs[head]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var cs []map[string]any
		for sha != "" && sha != f.refs["main"] {
			c := f.commits[sha]
			cs = append([]map[string]any{{"sha": sha, "commit": map[string]any{"author": c["author"]}}}, cs...)
			parents, _ := c["parents"].([]any)
			sha = ""
			if len(parents) > 0 {
				sha = parents[0].(string)
			}
		}
		write(map[string]any{"commits": cs})
	case strings.HasPrefix(rest, "/git/ref/heads/"):
		sha, ok := f.refs[strings.TrimPrefix(rest, "/git/ref/heads/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(map[string]any{"object": map[string]any{"sha": sha}})
	case strings.HasPrefix(rest, "/git/commits/"):
		write(map[string]any{"tree": map[string]any{"sha": f.commits[strings.TrimPrefix(rest, "/git/commits/")]["tree"]}})
	case rest == "/git/blobs":
		b, _ := base64.StdEncoding.DecodeString(body["content"].(string))
		id := f.id("b")
		f.blobs[id] = string(b)
		write(map[string]any{"sha": id})
	case rest == "/git/trees":
		files := map[string]string{}
		for k, v := range f.trees[body["base_tree"].(string)] {
			files[k] = v
		}
		for _, e := range body["tree"].([]any) {
			ent := e.(map[string]any)
			if ent["sha"] == nil {
				delete(files, ent["path"].(string))
			} else {
				files[ent["path"].(string)] = ent["sha"].(string)
			}
		}
		id := f.id("t")
		f.trees[id] = files
		write(map[string]any{"sha": id})
	case rest == "/git/commits":
		id := f.id("c")
		f.commits[id] = map[string]any{"tree": body["tree"], "parents": body["parents"], "author": body["author"]}
		write(map[string]any{"sha": id})
	case rest == "/git/refs" && r.Method == http.MethodPost:
		f.refs[strings.TrimPrefix(body["ref"].(string), "refs/heads/")] = body["sha"].(string)
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{})
	case strings.HasPrefix(rest, "/git/refs/heads/") && r.Method == http.MethodPatch:
		if body["force"] != true {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		f.refs[strings.TrimPrefix(rest, "/git/refs/heads/")] = body["sha"].(string)
		write(map[string]any{})
	case rest == "/pulls" && r.Method == http.MethodPost:
		num := len(f.pulls) + 1
		p := map[string]any{"number": num, "html_url": fmt.Sprintf("https://github.com/acme/app/pull/%d", num), "title": body["title"], "body": body["body"],
			"state": "open", "head": map[string]any{"ref": body["head"]}, "base": map[string]any{"ref": body["base"]}, "labels": []any{}}
		f.pulls = append(f.pulls, p)
		w.WriteHeader(http.StatusCreated)
		write(p)
	case strings.HasPrefix(rest, "/pulls/") && r.Method == http.MethodPatch:
		num, _ := strconv.Atoi(strings.TrimPrefix(rest, "/pulls/"))
		p := f.pulls[num-1]
		for k, v := range body {
			p[k] = v
		}
		write(p)
	case strings.HasPrefix(rest, "/issues/") && strings.HasSuffix(rest, "/labels"):
		num, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "/issues/"), "/labels"))
		var labels []any
		for _, l := range body["labels"].([]any) {
			labels = append(labels, map[string]any{"name": l})
		}
		f.pulls[num-1]["labels"] = labels
		write(labels)
	case strings.HasPrefix(rest, "/issues/") && strings.HasSuffix(rest, "/comments"):
		id := strings.TrimSuffix(strings.TrimPrefix(rest, "/issues/"), "/comments")
		f.comments[id] = append(f.comments[id], body["body"].(string))
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{})
	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T) (*fakeGitHub, sdk.PlatformClient, func()) {
	fake := newFake()
	srv := httptest.NewServer(fake)
	fake.srvURL = srv.URL
	p := &Platform{HTTP: srv.Client()}
	c := sdktest.TestPlatform(t, p, sdk.PlatformConfig{APIURL: srv.URL, Repo: "acme/app", Token: "ghp_test"})
	return fake, c, srv.Close
}

func TestPublishFlow(t *testing.T) {
	fake, client, done := setup(t)
	defer done()
	ctx := context.Background()
	e, _ := engine.New(ctx, engine.Options{})
	cs := engine.ChangeSet{Project: sdk.Project{ID: "maven:."}, Group: "updates/g:lib", Branch: "deepward/maven/updates-lib", Title: "Deepward: update g:lib 1.0 → 1.1",
		Labels: []string{"dependencies"}, Items: []engine.FixItem{{Package: "pkg:maven/g/lib", From: "1.0", To: "1.1", Kind: sdk.KindOutdated}},
		Files: []sdk.FileChange{{Path: "pom.xml", Content: []byte("<project>1.1</project>")}, {Path: "docs/new file.md", Content: []byte("x")}}}
	plan := &engine.FixPlan{ChangeSets: []engine.ChangeSet{cs}, Result: &sdk.ScanResult{Projects: []sdk.ProjectResult{{Project: sdk.Project{ID: "maven:."}, Status: sdk.ProjectOK}}}}
	res, err := e.Publish(ctx, client, plan, "main")
	if err != nil || len(res.Created) != 1 || res.Created[0].Labels[0] != "dependencies" {
		t.Fatalf("create: %+v %v", res, err)
	}
	content, ok, _ := client.ReadFile(ctx, "deepward/maven/updates-lib", "pom.xml")
	if !ok || string(content) != "<project>1.1</project>" {
		t.Errorf("branch content: %q", content)
	}
	if _, ok, _ := client.ReadFile(ctx, "main", "missing"); ok {
		t.Error("missing file")
	}
	res, err = e.Publish(ctx, client, plan, "main")
	if err != nil || len(res.Updated) != 1 || len(fake.pulls) != 1 {
		t.Errorf("update: %+v %v", res, err)
	}
	// Someone pushes to the bot branch.
	human := fake.id("c")
	fake.commits[human] = map[string]any{"tree": "t0", "parents": []any{fake.refs["deepward/maven/updates-lib"]}, "author": map[string]any{"name": "Dev", "email": "dev@acme"}}
	fake.refs["deepward/maven/updates-lib"] = human
	res, _ = e.Publish(ctx, client, plan, "main")
	if len(res.Paused) != 1 || len(fake.comments["1"]) != 1 {
		t.Errorf("pause: %+v %v", res, fake.comments)
	}
	res, _ = e.Publish(ctx, client, &engine.FixPlan{Result: plan.Result}, "main")
	if len(res.Closed) != 1 || fake.pulls[0]["state"] != "closed" {
		t.Errorf("close: %+v", res)
	}
	if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: "deepward/maven/updates-lib", BaseBranch: "main"}); err == nil {
		t.Error("existing branch without force")
	}
	if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: "rm", BaseBranch: "main", Files: []sdk.FileChange{{Path: "pom.xml", Delete: true}}}); err != nil {
		t.Errorf("delete: %v", err)
	}
	if commits, err := client.BranchCommits(ctx, "none", "main"); commits != nil || err != nil {
		t.Error("missing branch")
	}
}

func TestPaginationAndErrors(t *testing.T) {
	fake, client, done := setup(t)
	defer done()
	ctx := context.Background()
	fake.pulls = []map[string]any{
		{"number": 1, "state": "open", "head": map[string]any{"ref": "deepward/a"}, "base": map[string]any{"ref": "main"}},
		{"number": 2, "state": "open", "head": map[string]any{"ref": "deepward/b"}, "base": map[string]any{"ref": "main"}},
	}
	crs, err := client.ListChangeRequests(ctx, "deepward/")
	if err != nil || len(crs) != 2 {
		t.Errorf("pagination: %+v %v", crs, err)
	}
	calls := map[string]func() error{
		"/pulls":     func() error { _, err := client.ListChangeRequests(ctx, ""); return err },
		"/compare/":  func() error { _, err := client.BranchCommits(ctx, "x", "main"); return err },
		"/contents/": func() error { _, _, err := client.ReadFile(ctx, "main", "x"); return err },
		"/git/ref/": func() error {
			_, err := client.Commit(ctx, sdk.CommitRequest{Branch: "z", BaseBranch: "main"})
			return err
		},
		"/git/commits/": func() error {
			_, err := client.Commit(ctx, sdk.CommitRequest{Branch: "z", BaseBranch: "main"})
			return err
		},
		"/git/blobs": func() error {
			_, err := client.Commit(ctx, sdk.CommitRequest{Branch: "z", BaseBranch: "main", Files: []sdk.FileChange{{Path: "a"}}})
			return err
		},
		"/git/trees": func() error {
			_, err := client.Commit(ctx, sdk.CommitRequest{Branch: "z", BaseBranch: "main"})
			return err
		},
		"/git/refs": func() error {
			_, err := client.Commit(ctx, sdk.CommitRequest{Branch: "zz", BaseBranch: "main"})
			return err
		},
		"/comments":       func() error { return client.Comment(ctx, "1", "x") },
		"/repos/acme/app": func() error { _, err := client.DefaultBranch(ctx); return err },
	}
	for fail, call := range calls {
		fake.failNext = fail
		if err := call(); err == nil {
			t.Errorf("%s failure must propagate", fail)
		}
		fake.failNext = ""
	}
	fake.failNext = "/labels"
	if _, err := client.CreateChangeRequest(ctx, sdk.ChangeRequestSpec{Title: "t", SourceBranch: "s", TargetBranch: "main", Labels: []string{"x"}}); err == nil {
		t.Error("label failure on create")
	}
	fake.failNext = "/labels"
	if _, err := client.UpdateChangeRequest(ctx, "1", sdk.ChangeRequestSpec{Labels: []string{"x"}}); err == nil {
		t.Error("label failure on update")
	}
	fake.failNext = "/pulls"
	if _, err := client.CreateChangeRequest(ctx, sdk.ChangeRequestSpec{}); err == nil {
		t.Error("create failure")
	}
	fake.failNext = "/pulls/1"
	if _, err := client.UpdateChangeRequest(ctx, "1", sdk.ChangeRequestSpec{}); err == nil {
		t.Error("update failure")
	}
	fake.failNext = "/pulls/1"
	if err := client.CloseChangeRequest(ctx, "1"); err == nil {
		t.Error("close failure")
	}
	bad := &Client{api: "http://[::1", repo: "/repos/a/b", token: "t", http: http.DefaultClient}
	if _, err := bad.DefaultBranch(ctx); err == nil {
		t.Error("bad url")
	}
	dead := &Client{api: "http://127.0.0.1:1", repo: "/repos/a/b", token: "t", http: http.DefaultClient}
	if _, err := dead.DefaultBranch(ctx); err == nil {
		t.Error("unreachable")
	}
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer garbage.Close()
	g := &Client{api: garbage.URL, repo: "/repos/a/b", token: "t", http: garbage.Client()}
	if _, err := g.DefaultBranch(ctx); err == nil {
		t.Error("garbage")
	}
	if _, err := g.do(ctx, http.MethodPost, "/x", "", map[string]any{"f": func() {}}, nil); err == nil {
		t.Error("unmarshalable body")
	}
}

func TestConnectAndDetect(t *testing.T) {
	p := &Platform{}
	if _, err := p.Connect(context.Background(), sdk.PlatformConfig{Repo: "a/b"}); err == nil {
		t.Error("token required")
	}
	for _, r := range []string{"", "a", "a/b/c", "/b"} {
		if _, err := p.Connect(context.Background(), sdk.PlatformConfig{Token: "t", Repo: r}); err == nil {
			t.Errorf("repo %q must be rejected", r)
		}
	}
	c, _ := p.Connect(context.Background(), sdk.PlatformConfig{Token: "t", Repo: "acme/app", RepoURL: "https://github.acme.local/acme/app"})
	if c.(*Client).api != "https://github.acme.local/api/v3" {
		t.Errorf("enterprise API: %s", c.(*Client).api)
	}
	for in, want := range map[string]string{"": DefaultAPI, "https://github.com/a/b": DefaultAPI, "git@ghe.corp:a/b.git": "https://ghe.corp/api/v3", "::": DefaultAPI} {
		if got := APIURL(in); got != want {
			t.Errorf("APIURL(%q) = %s", in, got)
		}
	}
	if !p.Detect("https://github.com/a/b") || !p.Detect("git@github.com:a/b.git") || p.Detect("https://gitlab.com/a/b") {
		t.Error("detect")
	}
}
