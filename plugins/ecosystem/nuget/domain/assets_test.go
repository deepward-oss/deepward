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
	g, err := BuildGraph("project:nuget:.", "test", []OwnedAssets{{Path: "App.csproj", Assets: a}})
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
	g, err := BuildGraph("project:x", "t", []OwnedAssets{{Path: "X.csproj", Assets: a}})
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

func assetsOf(t *testing.T, src string) *Assets {
	t.Helper()
	a, err := ParseAssets(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestModulesFromProjectReferences(t *testing.T) {
	core := assetsOf(t, `{"version":3,"targets":{"net8.0":{"Newtonsoft.Json/12.0.1":{"type":"package"}}},
"libraries":{"Newtonsoft.Json/12.0.1":{"type":"package","path":"newtonsoft.json/12.0.1"}},
"project":{"frameworks":{"net8.0":{"dependencies":{"Newtonsoft.Json":{"target":"Package","version":"[12.0.1, )"}}}}}}`)
	web := assetsOf(t, `{"version":3,"targets":{"net8.0":{
  "Core/1.0.0":{"type":"project","dependencies":{"Newtonsoft.Json":"12.0.1"}},
  "Ext/1.0.0":{"type":"project","dependencies":{"Polly":"8.0.0"}},
  "Newtonsoft.Json/12.0.1":{"type":"package"},"Serilog/3.0.0":{"type":"package"},"Polly/8.0.0":{"type":"package"}}},
"libraries":{"Core/1.0.0":{"type":"project","path":"../Core/Core.csproj"},"Ext/1.0.0":{"type":"project","path":"../../../elsewhere/Ext.csproj"},
  "Serilog/3.0.0":{"type":"package"},"Polly/8.0.0":{"type":"package"},"Newtonsoft.Json/12.0.1":{"type":"package"}},
"project":{"frameworks":{"net8.0":{"dependencies":{"Serilog":{"target":"Package"}}}}}}`)
	g, err := BuildGraph("project:nuget:.", "t", []OwnedAssets{{Path: "src/Core/Core.csproj", Assets: core}, {Path: "src/Web/Web.csproj", Assets: web}})
	if err != nil {
		t.Fatal(err)
	}
	const coreID, webID = "module:src/Core/Core.csproj", "module:src/Web/Web.csproj"
	if m, ok := g.Node(webID); !ok || !m.IsModule() || m.Name != "Web" || m.Attributes["manifest"] != "src/Web/Web.csproj" {
		t.Fatalf("Web module: %+v", m)
	}
	if !contains(g.Children(webID), coreID) {
		t.Errorf("Web references Core: %v", g.Children(webID))
	}
	nj, _ := g.Node("pkg:nuget/newtonsoft.json@12.0.1")
	if !nj.Direct || len(nj.Modules) != 2 || nj.Modules[coreID] != "compile" || nj.Modules[webID] != "compile" {
		t.Errorf("Newtonsoft.Json is declared by Core and flows into Web: %+v", nj)
	}
	if paths := g.Paths(nj.ID, 0); len(paths) == 0 || paths[0][0] != coreID {
		t.Errorf("paths start at Core: %v", paths)
	}
	if sl, _ := g.Node("pkg:nuget/serilog@3.0.0"); !sl.Direct || len(sl.Modules) != 1 {
		t.Errorf("Serilog only in Web: %+v", sl)
	}
	// Ext is outside the workspace, so not a module: Polly hangs off Web.
	if !contains(g.Children(webID), "pkg:nuget/polly@8.0.0") || len(g.Modules()) != 2 {
		t.Errorf("project outside the boundary: %v %d", g.Children(webID), len(g.Modules()))
	}
	// One project file: flat graph.
	flat, err := BuildGraph("project:nuget:src/Core", "t", []OwnedAssets{{Path: "src/Core/Core.csproj", Assets: core}})
	if err != nil || len(flat.Modules()) != 0 {
		t.Fatalf("flat: %v %d", err, len(flat.Modules()))
	}
	if n, _ := flat.Node("pkg:nuget/newtonsoft.json@12.0.1"); !n.Direct || n.Modules != nil {
		t.Errorf("flat node: %+v", n)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
