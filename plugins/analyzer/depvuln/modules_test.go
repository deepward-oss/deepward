package depvuln

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// moduleRequest has modules api (declares kafka-clients -> lz4) and app
// (declares commons -> kafka-clients); with flat, the same packages hang
// off the root.
func moduleRequest(t *testing.T, flat bool) sdk.AnalyzeRequest {
	t.Helper()
	const (
		api, app = "module:api/pom.xml", "module:app/pom.xml"
		commons  = "pkg:maven/com.acme/commons@2.2.0"
	)
	pkg := func(id, name, version string) deps.Node {
		return deps.Node{ID: id, Ecosystem: deps.Maven, Name: name, Version: version, Scope: "runtime"}
	}
	nodes := []deps.Node{pkg(kafka, "org.apache.kafka:kafka-clients", "4.4.0"), pkg(lz4, "at.yawk.lz4:lz4-java", "1.11.2"),
		pkg(commons, "com.acme:commons", "2.2.0")}
	edges := []deps.Edge{{From: kafka, To: lz4}, {From: commons, To: kafka}}
	if flat {
		edges = append(edges, deps.Edge{From: root, To: kafka}, deps.Edge{From: root, To: commons})
	} else {
		nodes = append(nodes, deps.Node{ID: api, Name: "api", Kind: deps.KindModule, Attributes: map[string]string{"manifest": "api/pom.xml"}},
			deps.Node{ID: app, Name: "app", Kind: deps.KindModule, Attributes: map[string]string{"manifest": "app/pom.xml"}})
		edges = append(edges, deps.Edge{From: root, To: api}, deps.Edge{From: root, To: app},
			deps.Edge{From: api, To: kafka}, deps.Edge{From: app, To: commons})
	}
	g, err := deps.NewGraph(root, "test", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"pom.xml":     {Data: []byte("<project>\n  <!-- kafka-clients version below -->\n</project>\n")},
		"api/pom.xml": {Data: []byte("<project>\n  <dependencies>\n    <artifactId>kafka-clients</artifactId>\n")},
		"app/pom.xml": {Data: []byte("<project>\n    <artifactId>commons</artifactId>\n")},
	}}
	return sdk.AnalyzeRequest{Project: &sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml", "api/pom.xml", "app/pom.xml"}},
		Workspace: ws, Facts: factsOf{deps.FactDependencyGraph: g}}
}

func TestFindingsInModules(t *testing.T) {
	analyze := func(flat bool) sdk.Finding {
		a := New()
		host := &sdktest.FakeHost{Run: sdk.RunSettings{Offline: false}, PluginsByKind: map[sdk.Kind][]sdk.Plugin{
			deps.KindAdvisorySource: {&depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: kafkaAdvisories()}}}}
		if err := a.Init(context.Background(), host); err != nil {
			t.Fatal(err)
		}
		found, err := a.Analyze(context.Background(), moduleRequest(t, flat))
		if err != nil {
			t.Fatal(err)
		}
		f := byRule(found)["CVE-LZ4-1"]
		f.Fingerprint = f.ComputeFingerprint()
		return f
	}
	f := analyze(false)
	loc := f.Locations[0]
	mods := loc.Package.Modules
	if len(mods) != 2 || mods[0].Name != "api" || mods[1].Name != "app" || mods[0].Scope != "runtime" || mods[0].Direct || mods[1].Direct {
		t.Errorf("affected modules: %+v", mods)
	}
	if loc.File.Path != "api/pom.xml" || loc.File.StartLine != 3 {
		t.Errorf("declared in the module POM, not the root: %+v", loc.File)
	}
	if len(loc.Package.Paths) == 0 || loc.Package.Paths[0][0] != "module:api/pom.xml" {
		t.Errorf("paths start at a module: %v", loc.Package.Paths)
	}
	flat := analyze(true)
	if flat.Fingerprint != f.Fingerprint || flat.Locations[0].Package.Modules != nil {
		t.Errorf("fingerprints do not depend on modules: %s vs %s", flat.Fingerprint, f.Fingerprint)
	}
	if flat.Locations[0].File.Path != "pom.xml" {
		t.Errorf("without modules the first mention is used, as before: %+v", flat.Locations[0].File)
	}
}

func TestPinTargetsEntryModules(t *testing.T) {
	a := New()
	host := &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {&depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: kafkaAdvisories()}}}}
	if err := a.Init(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	pins := func(flat bool) [][]string {
		found, err := a.Analyze(context.Background(), moduleRequest(t, flat))
		if err != nil {
			t.Fatal(err)
		}
		var out [][]string
		for _, r := range byRule(found)["CVE-LZ4-1"].Remediations {
			if r.Package != nil && r.Package.Pin {
				out = append(out, r.Package.Targets)
			}
		}
		return out
	}
	// lz4 enters through api (kafka-clients) and app (commons -> kafka-clients).
	if got := pins(false); len(got) != 1 || len(got[0]) != 2 || got[0][0] != "api/pom.xml" || got[0][1] != "app/pom.xml" {
		t.Errorf("pin targets: %v", got)
	}
	if got := pins(true); len(got) != 1 || got[0] != nil {
		t.Errorf("no targets without modules: %v", got)
	}
}
