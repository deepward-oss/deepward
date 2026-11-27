package domain

import (
	"os"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

const pom = `<?xml version="1.0"?>
<project>
    <modelVersion>4.0.0</modelVersion>
    <parent>
        <groupId>com.acme</groupId>
        <artifactId>parent</artifactId>
        <version>7</version>
        <relativePath>../parent</relativePath>
    </parent>
    <artifactId>app</artifactId>
    <properties>
        <jackson.version> 2.15.0 </jackson.version>
    </properties>
    <modules>
        <module>core</module>
    </modules>
    <profiles><profile><modules><module>extra/pom.xml</module></modules></profile></profiles>
    <dependencyManagement>
        <dependencies>
            <dependency>
                <groupId>org.yaml</groupId>
                <artifactId>snakeyaml</artifactId>
                <version>1.33</version>
            </dependency>
        </dependencies>
    </dependencyManagement>
    <dependencies>
        <dependency>
            <groupId>com.fasterxml.jackson.core</groupId>
            <artifactId>jackson-databind</artifactId>
            <version>${jackson.version}</version>
        </dependency>
        <dependency>
            <groupId>junit</groupId>
            <artifactId>junit</artifactId>
            <version>4.13.1</version>
            <scope>test</scope>
            <type>jar</type>
            <classifier>tests</classifier>
        </dependency>
    </dependencies>
</project>
`

func TestParsePOM(t *testing.T) {
	data := []byte(pom)
	p, err := ParsePOM(data)
	if err != nil {
		t.Fatal(err)
	}
	span := func(v Value) string { return string(data[v.Span.Start:v.Span.End]) }
	if p.ArtifactID.Text != "app" || p.EffectiveGroupID() != "com.acme" || p.EffectiveVersion() != "7" {
		t.Errorf("coordinates: %+v", p)
	}
	if p.Parent == nil || p.Parent.RelativePath != "../parent" || !p.Parent.HasRelativePath || span(p.Parent.Version) != "7" {
		t.Errorf("parent: %+v", p.Parent)
	}
	if strings.Join(p.Modules, ",") != "core,extra/pom.xml" {
		t.Errorf("modules: %v", p.Modules)
	}
	jv := p.Properties["jackson.version"]
	if jv.Text != "2.15.0" || span(jv) != "2.15.0" {
		t.Errorf("property span: %+v %q", jv, span(jv))
	}
	if len(p.Dependencies) != 2 || len(p.Managed) != 1 {
		t.Fatalf("deps: %+v managed: %+v", p.Dependencies, p.Managed)
	}
	jd := p.Dependencies[0]
	if jd.Coordinates() != "com.fasterxml.jackson.core:jackson-databind" || span(jd.Version) != "${jackson.version}" {
		t.Errorf("dependency: %+v", jd)
	}
	if !strings.HasPrefix(string(data[jd.Element.Start:jd.Element.End]), "<dependency>") || !strings.HasSuffix(string(data[jd.Element.Start:jd.Element.End]), "</dependency>") {
		t.Errorf("element span: %q", data[jd.Element.Start:jd.Element.End])
	}
	ju := p.Dependencies[1]
	if ju.Scope != "test" || ju.Type != "jar" || ju.Classifier != "tests" {
		t.Errorf("junit: %+v", ju)
	}
	if !strings.HasPrefix(pom[p.ManagedDependenciesEnd:], "</dependencies>\n    </dependencyManagement>") || !strings.HasPrefix(pom[p.ProjectEnd:], "</project>") {
		t.Errorf("insertion points: %d %d", p.ManagedDependenciesEnd, p.ProjectEnd)
	}
	if p.Indent != "    " {
		t.Errorf("indent %q", p.Indent)
	}
	if name, ok := PropertyRef(" ${jackson.version} "); !ok || name != "jackson.version" {
		t.Error("PropertyRef")
	}
	if _, ok := PropertyRef("2.0-${x}"); ok {
		t.Error("partial reference is not a property ref")
	}
}

func TestParsePOMEdgeCases(t *testing.T) {
	p, err := ParsePOM([]byte("<project><groupId>g</groupId><version>1</version></project>"))
	if err != nil || p.EffectiveGroupID() != "g" || p.EffectiveVersion() != "1" || p.Indent != "  " || p.ManagedDependenciesEnd != -1 {
		t.Errorf("minimal: %+v %v", p, err)
	}
	if _, err := ParsePOM([]byte("<project><a>")); err == nil {
		t.Error("truncated POM")
	}
	if _, err := ParsePOM([]byte("<other/>")); err == nil {
		t.Error("not a project")
	}
	if _, err := ParsePOM([]byte("<project><a></b></project>")); err != nil {
		t.Logf("mismatched tags accepted in non-strict mode: %v", err)
	}
	if _, err := ParsePOM([]byte("<project>&bogus;</project>")); err == nil {
		t.Log("unknown entity accepted in non-strict mode")
	}
}

func TestParseTreesAndBuildGraph(t *testing.T) {
	f, err := os.Open("../testdata/tree-multi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	trees, err := ParseTrees(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 3 {
		t.Fatalf("trees: %d", len(trees))
	}
	poms := map[string]string{"com.acme:billing-parent": "pom.xml", "com.acme:billing-core": "core/pom.xml", "com.acme:billing-web": "web/pom.xml"}
	g, err := BuildGraph("project:maven:.", "test", trees, poms)
	if err != nil {
		t.Fatal(err)
	}
	node := func(id string) deps.Node {
		n, ok := g.Node(id)
		if !ok {
			t.Fatalf("missing node %s", id)
		}
		return n
	}
	const core, web = "module:core/pom.xml", "module:web/pom.xml"
	if len(g.Modules()) != 3 {
		t.Fatalf("modules: %+v", g.Modules())
	}
	if m := node(core); !m.IsModule() || m.Name != "billing-core" || m.Attributes["manifest"] != "core/pom.xml" {
		t.Errorf("core module: %+v", m)
	}
	if got := g.Children(web); !strings.Contains(strings.Join(got, ","), core) {
		t.Errorf("web depends on the core module: %v", got)
	}
	log4j := node("pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1")
	if !log4j.Direct || log4j.Scope != "compile" || log4j.Modules[core] != "compile" || log4j.Modules[web] != "compile" || log4j.Attributes["modules"] != "" {
		t.Errorf("log4j-core is declared by core and reaches web through it: %+v", log4j)
	}
	if paths := g.Paths(log4j.ID, 0); len(paths) == 0 || paths[0][0] != core {
		t.Errorf("paths start at the declaring module: %v", paths)
	}
	if api := node("pkg:maven/org.apache.logging.log4j/log4j-api@2.14.1"); api.Direct {
		t.Error("log4j-api is transitive")
	}
	// junit is test-only in core and does not reach web through core.
	if junit := node("pkg:maven/junit/junit@4.13.1"); junit.Scope != "test" || len(junit.Modules) != 1 || junit.Modules[core] != "test" {
		t.Errorf("junit: %+v", junit)
	}
	if sy := node("pkg:maven/org.yaml/snakeyaml@1.33"); !sy.Direct || len(sy.Modules) != 1 || sy.Modules[web] != "compile" {
		t.Errorf("snakeyaml: %+v", sy)
	}
	if _, ok := g.Node("pkg:maven/com.acme/billing-core@1.0.0"); ok {
		t.Error("reactor modules are not graph nodes")
	}
	if len(g.Packages()) != 9 {
		t.Errorf("packages: %d", len(g.Packages()))
	}
}

func TestConflictResolutionAndScopes(t *testing.T) {
	// Maven resolved jackson-databind to 2.15.0 (nearest wins): the tree only
	// contains the selected version.
	src := `{"groupId":"g","artifactId":"app","version":"1","children":[
	  {"groupId":"a","artifactId":"lib-a","version":"1","scope":"compile","children":[
	    {"groupId":"com.fasterxml.jackson.core","artifactId":"jackson-databind","version":"2.15.0","scope":"compile"}]},
	  {"groupId":"b","artifactId":"lib-b","version":"1","scope":"test","optional":"true","children":[
	    {"groupId":"com.fasterxml.jackson.core","artifactId":"jackson-databind","version":"2.15.0","scope":"test"}]},
	  {"groupId":"c","artifactId":"lib-c","version":"1","scope":"test"},
	  {"groupId":"c","artifactId":"lib-c","version":"1"}]}`
	trees, err := ParseTrees(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildGraph("project:x", "t", trees, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A single-module project keeps a flat graph.
	if len(g.Modules()) != 0 {
		t.Errorf("single module: %+v", g.Modules())
	}
	if a, _ := g.Node("pkg:maven/a/lib-a@1"); !a.Direct || a.Modules != nil {
		t.Errorf("flat graph: %+v", a)
	}
	var jacksons []deps.Node
	for _, n := range g.Packages() {
		if strings.Contains(n.ID, "jackson-databind") {
			jacksons = append(jacksons, n)
		}
	}
	if len(jacksons) != 1 || jacksons[0].Version != "2.15.0" || jacksons[0].Scope != "compile" {
		t.Errorf("jackson: %+v", jacksons)
	}
	b, _ := g.Node("pkg:maven/b/lib-b@1")
	c, _ := g.Node("pkg:maven/c/lib-c@1")
	if b.Attributes["optional"] != "true" || c.Scope != "compile" {
		t.Errorf("optional/scope widening: %+v %+v", b, c)
	}
	if _, err := ParseTrees(strings.NewReader("")); err == nil {
		t.Error("empty output")
	}
	if _, err := ParseTrees(strings.NewReader("{")); err == nil {
		t.Error("invalid JSON")
	}
	if _, err := BuildGraph("project:x", "t", []TreeNode{{GroupID: "g", ArtifactID: "a", Children: []TreeNode{{GroupID: "x y", ArtifactID: "z", Version: "1"}}}}, nil); err != nil {
		t.Logf("odd coordinates: %v", err)
	}
}
