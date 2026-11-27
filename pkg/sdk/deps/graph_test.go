package deps

import (
	"fmt"
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
	// cats reaches log4j in one step, a in two: closest first, not alphabetical.
	if got := g.DirectAncestors(log4j); !reflect.DeepEqual(got, []string{cats, a}) {
		t.Errorf("ancestors: %v", got)
	}
	if got := g.Chain(a, log4j); !reflect.DeepEqual(got, []string{a, b, log4j}) {
		t.Errorf("chain: %v", got)
	}
	if got := g.Chain(a, a); !reflect.DeepEqual(got, []string{a}) {
		t.Errorf("chain to self: %v", got)
	}
	if g.Chain(cats, a) != nil || g.Chain(a, "missing") != nil {
		t.Error("unreachable chains are nil")
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

// moduleGraph is a reactor with modules api, domain and app (app depends on
// domain): api declares kafka (-> lz4), domain declares commons (-> kafka),
// mockito is a test library everywhere, jffi has a version per module.
func moduleGraph(t *testing.T) *Graph {
	t.Helper()
	const (
		root    = "project:maven:."
		api     = "module:api/pom.xml"
		domain  = "module:domain/pom.xml"
		app     = "module:app/pom.xml"
		kafka   = "pkg:maven/org.apache.kafka/kafka-clients@4.4.0"
		lz4     = "pkg:maven/at.yawk.lz4/lz4-java@1.11.2"
		commons = "pkg:maven/com.acme/commons@2.2.0"
		mockito = "pkg:maven/org.mockito/mockito-core@5.0.0"
		jffiNew = "pkg:maven/com.github.jnr/jffi@1.3.9"
		jffiOld = "pkg:maven/com.github.jnr/jffi@1.2.16"
		jackson = "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.22.4"
		junit   = "pkg:maven/junit/junit@4.13.1"
	)
	mod := func(id, name string) Node { return Node{ID: id, Name: name, Kind: KindModule} }
	pkg := func(id, scope string, modules map[string]string) Node {
		return Node{ID: id, Ecosystem: Maven, Name: id, Scope: scope, Modules: modules}
	}
	g, err := NewGraph(root, "test", []Node{
		mod(api, "api"), mod(domain, "domain"), mod(app, "app"),
		// What app gets through domain comes from the plugin (app's own tree).
		pkg(kafka, "runtime", map[string]string{app: "runtime"}), pkg(lz4, "runtime", map[string]string{app: "runtime"}),
		pkg(commons, "compile", map[string]string{app: "compile"}), pkg(junit, "test", nil),
		pkg(mockito, "test", map[string]string{api: "test", domain: "test", app: "test"}),
		pkg(jffiNew, "compile", nil), pkg(jffiOld, "compile", map[string]string{app: "compile"}),
		pkg(jackson, "compile", map[string]string{api: "compile", app: "test"}),
	}, []Edge{
		{root, api}, {root, domain}, {root, app}, {app, domain},
		{api, kafka}, {kafka, lz4}, {domain, commons}, {commons, kafka},
		{api, mockito}, {domain, mockito}, {app, mockito},
		{api, jffiNew}, {domain, jffiOld}, {api, jackson}, {app, jackson}, {domain, junit},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestModuleNodes(t *testing.T) {
	g := moduleGraph(t)
	if n := len(g.Modules()); n != 3 {
		t.Fatalf("modules: %d", n)
	}
	for _, n := range g.Packages() {
		if n.IsModule() {
			t.Errorf("Packages returned module %s", n.ID)
		}
	}
	if len(g.Packages()) != 8 {
		t.Errorf("packages: %d", len(g.Packages()))
	}
	node := func(id string) Node {
		n, ok := g.Node(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		return n
	}
	// A package declared by a module is direct; one reached through it is not.
	if !node("pkg:maven/org.apache.kafka/kafka-clients@4.4.0").Direct || node("pkg:maven/at.yawk.lz4/lz4-java@1.11.2").Direct {
		t.Error("direct relative to modules")
	}
	if node("module:domain/pom.xml").Direct || !node("module:app/pom.xml").IsModule() {
		t.Error("modules are never direct packages")
	}
	// Modules reaching a package, with scopes.
	lz4 := node("pkg:maven/at.yawk.lz4/lz4-java@1.11.2").Modules
	if !reflect.DeepEqual(lz4, map[string]string{"module:api/pom.xml": "runtime", "module:domain/pom.xml": "runtime", "module:app/pom.xml": "runtime"}) {
		t.Errorf("lz4 is reached by api, domain and app (through domain): %v", lz4)
	}
	// The walk does not cross modules: domain's test-only junit is not app's.
	if got := node("pkg:maven/junit/junit@4.13.1").Modules; !reflect.DeepEqual(got, map[string]string{"module:domain/pom.xml": "test"}) {
		t.Errorf("junit only in domain: %v", got)
	}
	if got := node("pkg:maven/org.mockito/mockito-core@5.0.0"); got.Scope != "test" || got.Modules["module:app/pom.xml"] != "test" {
		t.Errorf("test library: %+v", got)
	}
	if got := node("pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.22.4").Modules; got["module:api/pom.xml"] != "compile" || got["module:app/pom.xml"] != "test" || len(got) != 2 {
		t.Errorf("per-module scopes kept: %v", got)
	}
	if got := node("pkg:maven/com.github.jnr/jffi@1.3.9").Modules; len(got) != 1 || got["module:api/pom.xml"] == "" {
		t.Errorf("jffi 1.3.9 only in api: %v", got)
	}
	if got := node("pkg:maven/com.github.jnr/jffi@1.2.16").Modules; len(got) != 2 || got["module:domain/pom.xml"] == "" || got["module:app/pom.xml"] == "" {
		t.Errorf("jffi 1.2.16 in domain and app (through domain): %v", got)
	}
	// Paths start at a module; direct ancestors are never modules.
	paths := g.Paths("pkg:maven/at.yawk.lz4/lz4-java@1.11.2", 0)
	if strings.Join(paths[0], " → ") != "module:api/pom.xml → pkg:maven/org.apache.kafka/kafka-clients@4.4.0 → pkg:maven/at.yawk.lz4/lz4-java@1.11.2" {
		t.Errorf("path from a module: %v", paths)
	}
	if got := g.DirectAncestors("pkg:maven/at.yawk.lz4/lz4-java@1.11.2"); !reflect.DeepEqual(got, []string{"pkg:maven/org.apache.kafka/kafka-clients@4.4.0", "pkg:maven/com.acme/commons@2.2.0"}) {
		t.Errorf("direct ancestors: %v", got)
	}
	if got := g.DirectAncestors("pkg:maven/com.acme/commons@2.2.0"); !reflect.DeepEqual(got, []string{"pkg:maven/com.acme/commons@2.2.0"}) {
		t.Errorf("a direct package is its own ancestor: %v", got)
	}
}

func TestModuleIDValidation(t *testing.T) {
	if _, err := NewGraph(app, "", []Node{{ID: "module:x", Name: "x"}}, nil); err == nil {
		t.Error("a package node must not use the module: prefix")
	}
	if _, err := NewGraph(app, "", []Node{{ID: "x", Kind: KindModule}}, nil); err == nil {
		t.Error("a module node needs the module: prefix")
	}
	if ModuleID("core/pom.xml") != "module:core/pom.xml" {
		t.Error("ModuleID")
	}
	// Graphs without modules are unchanged: no Modules maps, root children direct.
	g := sample(t)
	for _, n := range g.Packages() {
		if n.Modules != nil {
			t.Errorf("%s has modules without module nodes", n.ID)
		}
	}
	if len(g.Modules()) != 0 {
		t.Error("no modules")
	}
}

func TestModuleRefsAndDeclaringModule(t *testing.T) {
	g := moduleGraph(t)
	const (
		kafka   = "pkg:maven/org.apache.kafka/kafka-clients@4.4.0"
		lz4     = "pkg:maven/at.yawk.lz4/lz4-java@1.11.2"
		commons = "pkg:maven/com.acme/commons@2.2.0"
		jackson = "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.22.4"
	)
	refs := g.ModuleRefs(kafka)
	var got []string
	for _, r := range refs {
		got = append(got, fmt.Sprintf("%s:%s:%v", r.Name, r.Scope, r.Direct))
	}
	if strings.Join(got, ",") != "api:runtime:true,app:runtime:false,domain:runtime:false" {
		t.Errorf("module refs: %v", got)
	}
	if g.ModuleRefs("missing") != nil || sample(t).ModuleRefs(log4j) != nil {
		t.Error("no refs without modules")
	}
	if m, ok := g.DeclaringModule(kafka, lz4); !ok || m.Name != "api" {
		t.Errorf("declaring module on the shortest path: %+v", m)
	}
	if m, ok := g.DeclaringModule(commons, lz4); !ok || m.Name != "domain" {
		t.Errorf("declaring module of commons: %+v", m)
	}
	if m, ok := g.DeclaringModule(jackson, lz4); !ok || m.Name != "api" {
		t.Errorf("fallback to the first declaring module by name: %+v", m)
	}
	if _, ok := g.DeclaringModule(lz4, lz4); ok {
		t.Error("lz4 is declared by no module")
	}
}

func TestEntryModules(t *testing.T) {
	const (
		root = "project:nuget:."
		web  = "module:Web/Web.csproj"
		api  = "module:Api/Api.csproj"
		core = "module:Core/Core.csproj"
		lib  = "pkg:nuget/Some.Lib@1.0.0"
		stj  = "pkg:nuget/System.Text.Json@6.0.0"
		own  = "pkg:nuget/Web.Only@2.0.0"
	)
	mod := func(id string) Node { return Node{ID: id, Name: id, Kind: KindModule} }
	pkg := func(id string) Node { return Node{ID: id, Ecosystem: NuGet, Name: id, Scope: "compile"} }
	g, err := NewGraph(root, "test", []Node{mod(web), mod(api), mod(core), pkg(lib), pkg(stj), pkg(own)},
		[]Edge{{root, web}, {root, api}, {root, core}, {web, core}, {api, core}, {core, lib}, {lib, stj}, {web, own}, {own, stj}})
	if err != nil {
		t.Fatal(err)
	}
	// stj enters through Core (via Some.Lib) and through Web (via Web.Only);
	// Api only reaches it through Core.
	if got := g.EntryModules(stj); !reflect.DeepEqual(got, []string{core, web}) {
		t.Errorf("entry modules: %v", got)
	}
	if got := g.EntryModules(lib); !reflect.DeepEqual(got, []string{core}) {
		t.Errorf("entry modules of a direct package: %v", got)
	}
	if got := sample(t).EntryModules(log4j); got != nil {
		t.Errorf("no modules, no entry modules: %v", got)
	}
}

func TestAffected(t *testing.T) {
	g := sample(t)
	want := []string{"pkg:maven/g/a", "pkg:maven/g/b", "pkg:maven/org.apache.logging.log4j/log4j-core"}
	if got := g.Affected(a); !reflect.DeepEqual(got, want) {
		t.Errorf("an upgrade of a affects a and what it brings (cycles stop): %v", got)
	}
	if got := g.Affected(log4j); !reflect.DeepEqual(got, []string{"pkg:maven/org.apache.logging.log4j/log4j-core"}) {
		t.Errorf("a leaf affects only itself: %v", got)
	}
	if got := g.Affected("not a purl"); !reflect.DeepEqual(got, []string{"not a purl"}) {
		t.Errorf("unknown id: %v", got)
	}
}
