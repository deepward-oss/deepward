package deps

import (
	"reflect"
	"strings"
	"testing"
)

const (
	app   = "project:app"
	a     = "pkg:maven/g/a@1"
	b     = "pkg:maven/g/b@1"
	log4j = "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"
	cats  = "pkg:maven/org.typelevel/cats-core_2.13@2.10.0"
)

func sample(t *testing.T) *Graph {
	t.Helper()
	nodes := []Node{
		{ID: a, Name: "g:a", Version: "1"}, {ID: b, Name: "g:b", Version: "1"},
		{ID: log4j, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1", Direct: true},
		{ID: cats, Name: "org.typelevel:cats-core_2.13", Version: "2.10.0", Scope: ScopeTest},
		{ID: a, Name: "dup"},
	}
	edges := []Edge{{app, a}, {a, b}, {b, log4j}, {app, cats}, {cats, log4j}, {app, a}, {b, b}}
	g, err := NewGraph(app, "test", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestNewGraphNormalizes(t *testing.T) {
	g := sample(t)
	if len(g.Nodes) != 5 || len(g.Edges) != 5 {
		t.Fatalf("nodes=%d edges=%d", len(g.Nodes), len(g.Edges))
	}
	n, ok := g.Node(log4j)
	if !ok || n.Direct {
		t.Error("log4j is transitive: Direct must be recomputed from edges")
	}
	c, _ := g.Node(cats)
	if !c.Direct || c.Attributes["scalaBase"] != "cats-core" || c.Attributes["scalaSuffix"] != "_2.13" {
		t.Errorf("cats node: %+v", c)
	}
	if c.PURL().Name != "cats-core_2.13" {
		t.Error("PURL")
	}
	if _, ok := g.Node("missing"); ok {
		t.Error("missing node")
	}
	if len(g.Packages()) != 4 || g.ReportFact() != g || g.Validate() != nil {
		t.Error("packages/report/validate")
	}
	if got := g.Children(app); !reflect.DeepEqual(got, []string{a, cats}) {
		t.Errorf("children: %v", got)
	}
}

func TestPathsAndAncestors(t *testing.T) {
	g := sample(t)
	paths := g.Paths(log4j, 0)
	if len(paths) != 2 {
		t.Fatalf("paths: %v", paths)
	}
	if strings.Join(paths[0], " → ") != cats+" → "+log4j || strings.Join(paths[1], " → ") != a+" → "+b+" → "+log4j {
		t.Errorf("shortest first: %v", paths)
	}
	if got := g.Paths(log4j, 1); len(got) != 1 {
		t.Errorf("limit: %v", got)
	}
	if g.Paths(app, 0) != nil || g.Paths("missing", 0) != nil {
		t.Error("root/missing have no paths")
	}
	if got := g.DirectAncestors(log4j); !reflect.DeepEqual(got, []string{a, cats}) {
		t.Errorf("ancestors: %v", got)
	}
	if got := g.DirectAncestors(a); !reflect.DeepEqual(got, []string{a}) {
		t.Errorf("direct node is its own ancestor: %v", got)
	}
}

func TestGraphErrors(t *testing.T) {
	if _, err := NewGraph(app, "", []Node{{ID: "not-a-purl"}}, nil); err == nil {
		t.Error("invalid purl")
	}
	if _, err := NewGraph(app, "", nil, []Edge{{From: "x", To: app}}); err == nil {
		t.Error("unknown source")
	}
	if _, err := NewGraph(app, "", nil, []Edge{{From: app, To: "y"}}); err == nil {
		t.Error("unknown target")
	}
	g := &Graph{Root: "", Nodes: []Node{{ID: a}}}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "no root") || !strings.Contains(err.Error(), "no version") {
		t.Errorf("validate: %v", err)
	}
	// Lazily indexed graph (e.g. decoded from JSON).
	lazy := &Graph{Root: app, Nodes: []Node{{ID: app}, {ID: a, Version: "1"}}, Edges: []Edge{{app, a}}}
	if len(lazy.Paths(a, 0)) != 1 || len(lazy.Children(app)) != 1 {
		t.Error("lazy index")
	}
}
