package domain

import (
	"os"
	"sort"
	"strings"
	"testing"
)

func TestRecordedGraph(t *testing.T) {
	list, err := os.Open("../testdata/list.json")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Close()
	mods, err := ParseList(list)
	if err != nil || len(mods) != 5 {
		t.Fatalf("list: %d %v", len(mods), err)
	}
	graph, _ := os.ReadFile("../testdata/graph.txt")
	g, err := BuildGraph("project:go:.", "test", mods, graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, n := range g.Packages() {
		lines = append(lines, n.ID+" "+n.Scope+map[bool]string{true: " direct"}[n.Direct])
	}
	for _, e := range g.Edges {
		if e.From != g.Root {
			lines = append(lines, e.From+" -> "+e.To)
		}
	}
	sort.Strings(lines)
	want := `pkg:golang/golang.org/x/text@v0.3.7 -> pkg:golang/golang.org/x/tools@v0.0.0-20180917221912-90fa682c2a6e
pkg:golang/golang.org/x/text@v0.3.7 compile direct
pkg:golang/golang.org/x/tools@v0.0.0-20180917221912-90fa682c2a6e compile
pkg:golang/gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405 compile
pkg:golang/gopkg.in/yaml.v2@v2.2.7 -> pkg:golang/gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405
pkg:golang/gopkg.in/yaml.v2@v2.2.7 compile direct`
	if got := strings.Join(lines, "\n"); got != want {
		t.Errorf("graph:\n%s", got)
	}
}

func TestGraphSelectionAndReplacements(t *testing.T) {
	list := `{"Path":"example.com/app","Main":true}
{"Path":"a.example/x","Version":"v1.2.0"}
{"Path":"b.example/y","Version":"v0.3.0","Indirect":true}
{"Path":"c.example/local","Version":"v1.0.0","Replace":{"Path":"../local"}}
{"Path":"d.example/fork","Version":"v1.0.0","Replace":{"Path":"github.com/me/fork","Version":"v1.0.1"}}
{"Path":"e.example/noversion"}`
	mods, err := ParseList(strings.NewReader(list))
	if err != nil {
		t.Fatal(err)
	}
	graph := `example.com/app a.example/x@v1.2.0
example.com/app b.example/y@v0.3.0
example.com/app c.example/local@v1.0.0
example.com/app d.example/fork@v1.0.0
a.example/x@v1.2.0 b.example/y@v0.2.0
a.example/x@v1.1.0 b.example/y@v0.1.0
c.example/local@v1.0.0 b.example/y@v0.3.0
d.example/fork@v1.0.0 a.example/x@v1.0.0
a.example/x@v1.2.0 unknown.example/z@v1.0.0
garbage
go@1.21 toolchain@go1.21.5`
	g, err := BuildGraph("project:go:.", "test", mods, []byte(graph), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range g.Edges {
		got = append(got, e.From+" -> "+e.To)
	}
	want := []string{
		"pkg:golang/a.example/x@v1.2.0 -> pkg:golang/b.example/y@v0.3.0",
		"pkg:golang/github.com/me/fork@v1.0.1 -> pkg:golang/a.example/x@v1.2.0",
		"project:go:. -> pkg:golang/a.example/x@v1.2.0",
		"project:go:. -> pkg:golang/github.com/me/fork@v1.0.1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("edges:\n%s", strings.Join(got, "\n"))
	}
	fork, ok := g.Node("pkg:golang/github.com/me/fork@v1.0.1")
	if !ok || fork.Attributes["replaces"] != "d.example/fork@v1.0.0" {
		t.Errorf("replacement: %+v", fork)
	}
	if n, _ := g.Node("pkg:golang/b.example/y@v0.3.0"); n.Direct {
		t.Error("indirect module must not be direct")
	}
	if _, err := ParseList(strings.NewReader(`{"Path":"x","Error":{"Err":"no network"}}`)); err == nil || !strings.Contains(err.Error(), "no network") {
		t.Errorf("module error: %v", err)
	}
	if _, err := ParseList(strings.NewReader(`{`)); err == nil {
		t.Error("broken json")
	}
	if _, err := BuildGraph("r", "s", nil, []byte(strings.Repeat("x", 17*1024*1024)), nil); err == nil {
		t.Error("line too long")
	}
}

func TestProxyRules(t *testing.T) {
	cases := map[string]string{
		"": "https://proxy.golang.org/,",
		"https://a.example/,https://b.example|https://c.example,direct,https://d": "https://a.example,|https://b.example,true|https://c.example,",
		"off":         "",
		",,https://x": "https://x,",
	}
	for in, want := range cases {
		var parts []string
		for _, p := range ParseGOPROXY(in) {
			s := p.URL + ","
			if p.FallbackOnError {
				s += "true"
			}
			parts = append(parts, s)
		}
		got := strings.Join(parts, "|")
		if in == "" {
			got = strings.Replace(got, "https://proxy.golang.org,", "https://proxy.golang.org/,", 1)
		}
		if got != want {
			t.Errorf("GOPROXY %q = %q, want %q", in, got, want)
		}
	}
	if !Private("", "git.acme.example/*", "git.acme.example/lib") || Private("", "", "x") || Private("other.example", "git.acme.example", "git.acme.example/lib") {
		t.Error("private patterns")
	}
	if u, err := ListURL("https://proxy", "github.com/BurntSushi/toml"); err != nil || u != "https://proxy/github.com/!burnt!sushi/toml/@v/list" {
		t.Errorf("list url: %s %v", u, err)
	}
	if _, err := ListURL("https://proxy", "bad path with spaces"); err == nil {
		t.Error("invalid module path")
	}
	if CanonicalVersion("1.2.3") != "v1.2.3" || CanonicalVersion("v1") != "v1" {
		t.Error("canonical")
	}
}

func TestScopes(t *testing.T) {
	built := ModuleList([]byte("example.com/app\ngolang.org/x/text\n\n"))
	withTests := ModuleList([]byte("example.com/app\ngolang.org/x/text\ngopkg.in/check.v1\n"))
	scopes := Scopes(built, withTests)
	if scopes["golang.org/x/text"] != "compile" || scopes["gopkg.in/check.v1"] != "test" || len(scopes) != 3 {
		t.Errorf("scopes: %v", scopes)
	}
	if Scopes(nil, withTests) != nil {
		t.Error("nothing built means unknown, not all hidden")
	}
	list, _ := os.Open("../testdata/list.json")
	defer list.Close()
	mods, _ := ParseList(list)
	graph, _ := os.ReadFile("../testdata/graph.txt")
	g, err := BuildGraph("project:go:.", "test", mods, graph, scopes)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"pkg:golang/golang.org/x/text@v0.3.7":                              "compile",
		"pkg:golang/gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405":  "test",
		"pkg:golang/golang.org/x/tools@v0.0.0-20180917221912-90fa682c2a6e": "not-built",
		"pkg:golang/gopkg.in/yaml.v2@v2.2.7":                               "not-built",
	}
	for id, scope := range want {
		if n, ok := g.Node(id); !ok || n.Scope != scope {
			t.Errorf("%s: %q, want %s", id, n.Scope, scope)
		}
	}
}
