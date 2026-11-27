package domain

import (
	"os"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../testdata/graph.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildGraphFromRealOutput(t *testing.T) {
	reports, err := ParseReports(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildGraph("project:sbt:.", "test", reports)
	if err != nil {
		t.Fatal(err)
	}
	node := func(id string) deps.Node {
		n, ok := g.Node(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		return n
	}
	const core, root = "module:core", "module:root"
	if len(g.Modules()) != 2 || node(core).Name != "core" || !node(root).IsModule() {
		t.Fatalf("subprojects are modules: %+v", g.Modules())
	}
	if got := g.Children(root); !strings.Contains(strings.Join(got, ","), core) {
		t.Errorf("root depends on core (dependsOn): %v", got)
	}
	// cats-core is declared by core and reaches root through it, compile in both.
	cats := node("pkg:maven/org.typelevel/cats-core_2.13@2.9.0")
	if !cats.Direct || cats.Attributes["scalaBase"] != "cats-core" || cats.Attributes["scalaSuffix"] != "_2.13" ||
		cats.Modules[core] != deps.ScopeCompile || cats.Modules[root] != deps.ScopeCompile || cats.Attributes["projects"] != "" {
		t.Errorf("cats-core: %+v", cats)
	}
	if paths := g.Paths(cats.ID, 0); len(paths) == 0 || paths[0][0] != core {
		t.Errorf("paths start at a subproject: %v", paths)
	}
	if kernel := node("pkg:maven/org.typelevel/cats-kernel_2.13@2.9.0"); kernel.Direct {
		t.Error("cats-kernel is transitive")
	}
	// scalatest is a test dependency of core only.
	if st := node("pkg:maven/org.scalatest/scalatest_2.13@3.2.15"); st.Scope != deps.ScopeTest || len(st.Modules) != 1 || st.Modules[core] != deps.ScopeTest {
		t.Errorf("scalatest: %+v", st)
	}
	if jd := node("pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1"); !jd.Direct || len(jd.Modules) != 1 || jd.Modules[root] != deps.ScopeCompile {
		t.Errorf("jackson: %+v", jd)
	}
	if _, ok := g.Node("pkg:maven/com.acme/core_2.13@0.1.0"); ok {
		t.Error("build modules are not nodes")
	}
	if log := node("pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"); log.Scope != deps.ScopeCompile {
		t.Errorf("log4j scope widened to compile: %s", log.Scope)
	}
}

func TestReportEdgeCases(t *testing.T) {
	src := "{\"project\":\"p\",\"module\":\"g:p:1\",\"scalaBinaryVersion\":\"3\",\"configurations\":[{\"name\":\"compile\",\"modules\":[{\"id\":\"a:lib_3:1.0\",\"callers\":[\"g:p_3:1\"]},{\"id\":\"a:old_3:0.9\",\"evicted\":true,\"callers\":[\"g:p_3:1\"]},{\"id\":\"a:dep:2.0\",\"callers\":[\"a:lib_3:1.0\",\"a:old_3:0.9\",\"weird\"]},{\"id\":\"broken\",\"callers\":[\"g:p_3:1\"]},{\"id\":\"a:norev:\",\"callers\":[\"g:p_3:1\"]}]},{\"name\":\"scala-tool\",\"modules\":[{\"id\":\"org.scala-lang:scala3-compiler_3:3.3.0\",\"callers\":[\"g:p_3:1\"]}]}]}" + "\n\n"
	reports, err := ParseReports(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildGraph("project:x", "t", reports)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Packages()) != 2 {
		t.Errorf("packages: %+v", g.Packages())
	}
	if _, ok := g.Node("pkg:maven/a/old_3@0.9"); ok {
		t.Error("evicted modules are skipped")
	}
	if n, _ := g.Node("pkg:maven/a/lib_3@1.0"); n.Attributes["scalaBinaryVersions"] != "3" {
		t.Errorf("attributes: %+v", n.Attributes)
	}
	// One subproject: a flat graph without modules.
	if n, _ := g.Node("pkg:maven/a/lib_3@1.0"); len(g.Modules()) != 0 || !n.Direct || n.Modules != nil {
		t.Errorf("single subproject: %+v %+v", g.Modules(), n)
	}
	if _, err := ParseReports(strings.NewReader("")); err == nil {
		t.Error("empty output")
	}
	if _, err := ParseReports(strings.NewReader("{bad\n")); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("bad line: %v", err)
	}
}
