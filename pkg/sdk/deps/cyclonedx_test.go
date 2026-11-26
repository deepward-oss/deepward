package deps

import (
	"strings"
	"testing"
)

const bom = `{
  "bomFormat": "CycloneDX", "specVersion": "1.5",
  "metadata": {"component": {"bom-ref": "app-ref", "name": "billing", "purl": "pkg:maven/com.acme/billing@1.0"}},
  "components": [
    {"bom-ref": "a", "purl": "pkg:maven/org.springframework/spring-web@5.3.0?type=jar"},
    {"bom-ref": "b", "purl": "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1", "scope": "required",
     "components": [{"bom-ref": "c", "purl": "pkg:maven/junit/junit@4.13", "scope": "excluded"}]},
    {"bom-ref": "d", "name": "no-purl"},
    {"bom-ref": "e", "purl": "pkg:pypi/left-pad@1.0.0"},
    {"bom-ref": "f", "purl": "garbage"},
    {"bom-ref": "g", "purl": "pkg:nuget/Newtonsoft.Json", "version": "13.0.1", "scope": "optional"}
  ],
  "dependencies": [
    {"ref": "app-ref", "dependsOn": ["a", "c", "g", "missing"]},
    {"ref": "a", "dependsOn": ["b"]},
    {"ref": "unknown", "dependsOn": ["a"]}
  ]
}`

func TestParseCycloneDX(t *testing.T) {
	g, err := ParseCycloneDX(strings.NewReader(bom), "cyclonedx:bom.json")
	if err != nil {
		t.Fatal(err)
	}
	if g.Root != "project:billing" || g.Source != "cyclonedx:bom.json" || len(g.Packages()) != 4 {
		t.Fatalf("graph: root=%s packages=%+v", g.Root, g.Packages())
	}
	web, _ := g.Node("pkg:maven/org.springframework/spring-web@5.3.0")
	jackson, _ := g.Node("pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1")
	junit, _ := g.Node("pkg:maven/junit/junit@4.13")
	nuget, _ := g.Node("pkg:nuget/newtonsoft.json@13.0.1")
	if !web.Direct || jackson.Direct || junit.Scope != ScopeTest || nuget.Scope != "optional" || nuget.Ecosystem != NuGet || web.Name != "org.springframework:spring-web" {
		t.Errorf("nodes: %+v %+v %+v %+v", web, jackson, junit, nuget)
	}
	if p := g.Paths(jackson.ID, 0); len(p) != 1 || len(p[0]) != 2 {
		t.Errorf("paths: %v", p)
	}
}

func TestParseCycloneDXWithoutDependencies(t *testing.T) {
	g, err := ParseCycloneDX(strings.NewReader(`{"bomFormat":"CycloneDX","specVersion":"1.4","components":[{"purl":"pkg:maven/a/b@1"}]}`), "x")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := g.Node("pkg:maven/a/b@1")
	if g.Root != "project:root" || !n.Direct {
		t.Errorf("graph: %+v", g)
	}
}

func TestParseCycloneDXErrors(t *testing.T) {
	for in, want := range map[string]string{
		"{":                    "invalid CycloneDX JSON",
		`{"bomFormat":"SPDX"}`: "not a CycloneDX BOM",
		`{"bomFormat":"CycloneDX","specVersion":"1.3"}`: "need 1.4 or later",
		`{"bomFormat":"CycloneDX","specVersion":"x.y"}`: "need 1.4 or later",
		`{"bomFormat":"CycloneDX","specVersion":"2"}`:   "need 1.4 or later",
	} {
		if _, err := ParseCycloneDX(strings.NewReader(in), ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", in, err)
		}
	}
	if !specAtLeast("2.0", 1, 4) || specAtLeast("1.3", 1, 4) {
		t.Error("specAtLeast")
	}
}
