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
	cats := node("pkg:maven/org.typelevel/cats-core_2.13@2.9.0")
	if !cats.Direct || cats.Attributes["scalaBase"] != "cats-core" || cats.Attributes["scalaSuffix"] != "_2.13" || cats.Attributes["projects"] != "core,root" {
		t.Errorf("cats-core: %+v", cats)
	}
	if kernel := node("pkg:maven/org.typelevel/cats-kernel_2.13@2.9.0"); kernel.Direct {
		t.Error("cats-kernel is transitive")
	}
	if st := node("pkg:maven/org.scalatest/scalatest_2.13@3.2.15"); st.Scope != deps.ScopeTest {
		t.Errorf("scalatest scope: %s", st.Scope)
	}
	if jd := node("pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1"); !jd.Direct || jd.Attributes["projects"] != "root" {
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
	if _, err := ParseReports(strings.NewReader("")); err == nil {
		t.Error("empty output")
	}
	if _, err := ParseReports(strings.NewReader("{bad\n")); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("bad line: %v", err)
	}
}
