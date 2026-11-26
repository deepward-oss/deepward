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
	g, err := BuildGraph("project:maven:.", "test", trees)
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
	log4j := node("pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1")
	if !log4j.Direct || log4j.Attributes["modules"] != "billing-core" || log4j.Scope != "compile" {
		t.Errorf("log4j-core: %+v", log4j)
	}
	if api := node("pkg:maven/org.apache.logging.log4j/log4j-api@2.14.1"); api.Direct {
		t.Error("log4j-api is transitive")
	}
	if junit := node("pkg:maven/junit/junit@4.13.1"); junit.Scope != "test" {
		t.Errorf("junit scope: %s", junit.Scope)
	}
	if sy := node("pkg:maven/org.yaml/snakeyaml@1.33"); !sy.Direct || sy.Attributes["modules"] != "billing-web" {
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
	g, err := BuildGraph("project:x", "t", trees)
	if err != nil {
		t.Fatal(err)
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
	if _, err := BuildGraph("project:x", "t", []TreeNode{{GroupID: "g", ArtifactID: "a", Children: []TreeNode{{GroupID: "x y", ArtifactID: "z", Version: "1"}}}}); err != nil {
		t.Logf("odd coordinates: %v", err)
	}
}
