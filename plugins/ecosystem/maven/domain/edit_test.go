package domain

import (
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/textedit"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

const parentPOM = `<project>
  <groupId>com.acme</groupId>
  <artifactId>parent</artifactId>
  <version>1</version>
  <modules><module>core</module></modules>
  <properties>
    <jackson.version>2.15.0</jackson.version>
    <alias.version>${jackson.version}</alias.version>
  </properties>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>org.yaml</groupId>
        <artifactId>snakeyaml</artifactId>
        <version>1.33</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>
`

const corePOM = `<project>
  <parent>
    <groupId>com.acme</groupId>
    <artifactId>parent</artifactId>
    <version>1</version>
  </parent>
  <artifactId>core</artifactId>
  <dependencies>
    <dependency>
      <groupId>com.fasterxml.jackson.core</groupId>
      <artifactId>jackson-databind</artifactId>
      <version>${jackson.version}</version>
    </dependency>
    <dependency>
      <groupId>com.fasterxml.jackson.core</groupId>
      <artifactId>jackson-core</artifactId>
      <version>${alias.version}</version>
    </dependency>
    <dependency>
      <groupId>org.yaml</groupId>
      <artifactId>snakeyaml</artifactId>
    </dependency>
    <dependency>
      <groupId>commons-io</groupId>
      <artifactId>commons-io</artifactId>
      <version>2.7</version>
    </dependency>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-api</artifactId>
      <version>${slf4j.version}</version>
    </dependency>
    <dependency>
      <groupId>x</groupId>
      <artifactId>computed</artifactId>
      <version>1.0-${build.suffix}</version>
    </dependency>
    <dependency>
      <groupId>x</groupId>
      <artifactId>selfref</artifactId>
      <version>${project.version}</version>
    </dependency>
    <dependency>
      <groupId>org.springframework</groupId>
      <artifactId>spring-core</artifactId>
    </dependency>
  </dependencies>
</project>
`

func files(t *testing.T, contents map[string]string) []File {
	t.Helper()
	var out []File
	for _, p := range []string{"pom.xml", "core/pom.xml"} {
		c, ok := contents[p]
		if !ok {
			continue
		}
		pom, err := ParsePOM([]byte(c))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, File{Path: p, Data: []byte(c), POM: pom})
	}
	return out
}

func apply(t *testing.T, fs []File, edits []sdk.TextEdit) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range fs {
		var mine []sdk.TextEdit
		for _, e := range edits {
			if e.Path == f.Path {
				mine = append(mine, e)
			}
		}
		b, err := textedit.Apply(f.Data, mine)
		if err != nil {
			t.Fatal(err)
		}
		out[f.Path] = string(b)
	}
	return out
}

func TestPlanEdits(t *testing.T) {
	fs := files(t, map[string]string{"pom.xml": parentPOM, "core/pom.xml": corePOM})
	plan := func(g, a, to string, pin bool) ([]sdk.TextEdit, *sdk.ManualChange) {
		return Plan(fs, "pom.xml", sdk.PackageChange{PURL: "pkg:maven/" + g + "/" + a, To: to, Pin: pin, Reason: "CVE-2020-36518 -- fix"}, g, a)
	}
	edits, m := plan("com.fasterxml.jackson.core", "jackson-databind", "2.15.4", false)
	if m != nil || len(edits) != 1 || edits[0].Path != "pom.xml" {
		t.Fatalf("property edit: %+v %+v", edits, m)
	}
	out := apply(t, fs, edits)
	if !strings.Contains(out["pom.xml"], "<jackson.version>2.15.4</jackson.version>") || out["core/pom.xml"] != corePOM {
		t.Errorf("single-line property change:\n%s", out["pom.xml"])
	}
	if diff := textedit.Unified("pom.xml", []byte(parentPOM), []byte(out["pom.xml"])); strings.Count(diff, "\n-") != 1 {
		t.Errorf("diff must be a single line:\n%s", diff)
	}
	edits, _ = plan("com.fasterxml.jackson.core", "jackson-core", "2.15.4", false)
	if len(edits) != 1 || edits[0].Expected != "2.15.0" {
		t.Errorf("chained property: %+v", edits)
	}
	edits, _ = plan("commons-io", "commons-io", "2.14.0", false)
	if out := apply(t, fs, edits); !strings.Contains(out["core/pom.xml"], "<version>2.14.0</version>") {
		t.Error("literal edit")
	}
	edits, _ = plan("org.yaml", "snakeyaml", "2.0", false)
	if len(edits) != 1 || edits[0].Path != "pom.xml" {
		t.Errorf("managed version edited in dependencyManagement: %+v", edits)
	}
	for _, c := range []struct{ g, a, want string }{
		{"org.slf4j", "slf4j-api", "defined outside the repository"},
		{"x", "computed", "computed expression"},
		{"x", "selfref", "${project.version}"},
		{"org.springframework", "spring-core", "managed outside the repository"},
		{"not", "declared", "not declared"},
	} {
		_, m := plan(c.g, c.a, "9", false)
		if m == nil || !strings.Contains(m.Reason, c.want) {
			t.Errorf("%s:%s: %+v", c.g, c.a, m)
		}
	}
}

func TestPin(t *testing.T) {
	fs := files(t, map[string]string{"pom.xml": parentPOM, "core/pom.xml": corePOM})
	edits, m := Plan(fs, "pom.xml", sdk.PackageChange{To: "2.0", Pin: true, Reason: "GHSA-x -- y"}, "org.yaml", "snakeyaml-engine")
	if m != nil {
		t.Fatal(m)
	}
	out := apply(t, fs, edits)["pom.xml"]
	want := "      <!-- Deepward: pinned transitive dependency: GHSA-x - y -->\n      <dependency>\n        <groupId>org.yaml</groupId>\n        <artifactId>snakeyaml-engine</artifactId>\n        <version>2.0</version>\n      </dependency>\n    </dependencies>\n  </dependencyManagement>"
	if !strings.Contains(out, want) {
		t.Errorf("pin into existing dependencyManagement:\n%s", out)
	}
	if _, err := ParsePOM([]byte(out)); err != nil {
		t.Errorf("result must stay valid XML: %v", err)
	}
	crlf := strings.ReplaceAll("<project>\n\t<artifactId>a</artifactId>\n</project>\n", "\n", "\r\n")
	fs2 := files(t, map[string]string{"pom.xml": crlf})
	edits, _ = Plan(fs2, "pom.xml", sdk.PackageChange{To: "2.0", Pin: true}, "org.yaml", "snakeyaml")
	out = apply(t, fs2, edits)["pom.xml"]
	if !strings.Contains(out, "\t<dependencyManagement>\r\n\t\t<dependencies>\r\n\t\t\t<!-- Deepward: pinned transitive dependency: fixes a vulnerable transitive dependency -->\r\n\t\t\t<dependency>\r\n") ||
		!strings.HasSuffix(out, "\t</dependencyManagement>\r\n</project>\r\n") {
		t.Errorf("new dependencyManagement with CRLF and tabs:\n%q", out)
	}
	if _, m := Plan(nil, "pom.xml", sdk.PackageChange{Pin: true}, "g", "a"); m == nil {
		t.Error("missing root")
	}
}

func TestPropertyResolutionLimits(t *testing.T) {
	loop := `<project><artifactId>p</artifactId><properties><a>${b}</a><b>${a}</b></properties>
<dependencies><dependency><groupId>g</groupId><artifactId>x</artifactId><version>${a}</version></dependency></dependencies></project>`
	fs := files(t, map[string]string{"pom.xml": loop})
	if _, m := Plan(fs, "pom.xml", sdk.PackageChange{To: "2"}, "g", "x"); m == nil || !strings.Contains(m.Reason, "nested too deeply") {
		t.Errorf("cycle: %+v", m)
	}
	orphan := strings.Replace(corePOM, "<artifactId>parent</artifactId>", "<artifactId>other-parent</artifactId>", 1)
	fs = files(t, map[string]string{"pom.xml": parentPOM, "core/pom.xml": orphan})
	if _, m := Plan(fs, "pom.xml", sdk.PackageChange{To: "2"}, "com.fasterxml.jackson.core", "jackson-databind"); m == nil {
		t.Error("parent with different coordinates is not followed")
	}
	rel := strings.Replace(corePOM, "<version>1</version>\n  </parent>", "<version>1</version>\n    <relativePath/>\n  </parent>", 1)
	fs = files(t, map[string]string{"pom.xml": parentPOM, "core/pom.xml": rel})
	if _, m := Plan(fs, "pom.xml", sdk.PackageChange{To: "2"}, "com.fasterxml.jackson.core", "jackson-databind"); m == nil {
		t.Error("empty relativePath means the parent comes from a repository")
	}
	dir := strings.Replace(corePOM, "<version>1</version>\n  </parent>", "<version>1</version>\n    <relativePath>..</relativePath>\n  </parent>", 1)
	fs = files(t, map[string]string{"pom.xml": parentPOM, "core/pom.xml": dir})
	if _, m := Plan(fs, "pom.xml", sdk.PackageChange{To: "2"}, "com.fasterxml.jackson.core", "jackson-databind"); m != nil {
		t.Errorf("relativePath to a directory: %+v", m)
	}
	broken := []File{{Path: "pom.xml", Data: []byte("<project>")}}
	if _, m := Plan(broken, "pom.xml", sdk.PackageChange{}, "g", "a"); m == nil {
		t.Error("unparsable POMs are skipped")
	}
	if _, _, ok := findProperty(map[string]*File{}, "missing.xml", "x"); ok {
		t.Error("missing file")
	}
}
