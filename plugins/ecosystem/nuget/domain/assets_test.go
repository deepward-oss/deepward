package domain

import (
	"os"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

func TestRealAssets(t *testing.T) {
	f, err := os.Open("../testdata/project.assets.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a, err := ParseAssets(f)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildGraph("project:nuget:.", "test", []*Assets{a})
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
	az := node("pkg:nuget/azure.identity@1.10.0")
	if !az.Direct || az.Name != "Azure.Identity" || az.Ecosystem != deps.NuGet || az.Attributes["targetFrameworks"] != "net8.0" {
		t.Errorf("Azure.Identity: %+v", az)
	}
	if nj := node("pkg:nuget/newtonsoft.json@12.0.3"); !nj.Direct {
		t.Error("Newtonsoft.Json is direct")
	}
	msal := node("pkg:nuget/microsoft.identity.client@4.54.1")
	if msal.Direct {
		t.Error("MSAL is transitive")
	}
	if p := g.Paths(msal.ID, 0); len(p) == 0 || p[0][0] != az.ID {
		t.Errorf("path: %v", p)
	}
	if len(g.Packages()) != 20 {
		t.Errorf("packages: %d", len(g.Packages()))
	}
}

func TestProjectReferencesAndRIDs(t *testing.T) {
	src := `{"version":3,"targets":{
	  "net8.0":{"Lib/1.0.0":{"type":"project","dependencies":{"Serilog":"3.1.1"}},"Serilog/3.1.1":{"type":"package"},"Odd":{"type":"package"}},
	  "net8.0/linux-x64":{"Serilog/3.1.1":{"type":"package","dependencies":{"Missing":"1.0"}},"runtime.native/1.0":{"type":"package"}},
	  "net6.0":{"Serilog/3.1.1":{"type":"package"}}},
	 "project":{"frameworks":{"net8.0":{"dependencies":{"Lib":{"target":"Project"},"Serilog":{"target":"Package","version":"[3.1.1, )"}}},
	  "net6.0":{"dependencies":{"serilog":{"version":"[3.1.1, )"}}}}}}`
	a, err := ParseAssets(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildGraph("project:x", "t", []*Assets{a})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := g.Node("pkg:nuget/serilog@3.1.1")
	if !s.Direct || s.Attributes["targetFrameworks"] != "net6.0,net8.0" {
		t.Errorf("serilog: %+v", s)
	}
	if _, ok := g.Node("pkg:nuget/lib@1.0.0"); ok {
		t.Error("project references are not nodes")
	}
	if rt, _ := g.Node("pkg:nuget/runtime.native@1.0"); rt.Direct {
		t.Error("RID-specific package without a parent is not direct")
	}
	for _, bad := range []string{"{", `{"version":2,"targets":{}}`, `{"version":3}`} {
		if _, err := ParseAssets(strings.NewReader(bad)); err == nil {
			t.Errorf("%s must fail", bad)
		}
	}
	if _, _, ok := splitKey("noslash"); ok {
		t.Error("splitKey")
	}
}
