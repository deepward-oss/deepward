package domain

import (
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/textedit"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

const csproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <SerilogVersion>3.0.1</SerilogVersion>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="12.0.3" />
    <PackageReference Include="Serilog" Version="$(SerilogVersion)" />
    <PackageReference Update="Polly">
      <Version>7.2.3</Version>
    </PackageReference>
    <PackageReference Include="Dapper" VersionOverride="2.0.0" />
    <PackageReference Include="Range.Lib" Version="[1.0,2.0)" />
    <PackageReference Include="Missing.Prop" Version="$(Nope)" />
    <PackageReference Include="Azure.Identity" />
  </ItemGroup>
</Project>
`

const props = `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
    <CentralPackageTransitivePinningEnabled>true</CentralPackageTransitivePinningEnabled>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Azure.Identity" Version="1.10.0" />
  </ItemGroup>
</Project>
`

func run(t *testing.T, files []File, projects []string, propsFile string, to string, pin bool, id string) (map[string]string, *sdk.ManualChange) {
	t.Helper()
	edits, m := Plan(files, projects, propsFile, sdk.PackageChange{To: to, Pin: pin, Reason: "GHSA-1 -- fix"}, id)
	if m != nil {
		return nil, m
	}
	out := map[string]string{}
	for _, f := range files {
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
	return out, nil
}

func TestPlanNuGet(t *testing.T) {
	files := []File{{Path: "App.csproj", Data: []byte(csproj)}, {Path: "Directory.Packages.props", Data: []byte(props)}}
	projects := []string{"App.csproj"}
	cases := []struct{ id, to, file, want string }{
		{"newtonsoft.json", "13.0.1", "App.csproj", `<PackageReference Include="Newtonsoft.Json" Version="13.0.1" />`},
		{"Serilog", "3.1.1", "App.csproj", `<SerilogVersion>3.1.1</SerilogVersion>`},
		{"Polly", "7.2.4", "App.csproj", `<Version>7.2.4</Version>`},
		{"Dapper", "2.1.35", "App.csproj", `VersionOverride="2.1.35"`},
		{"Azure.Identity", "1.12.0", "Directory.Packages.props", `<PackageVersion Include="Azure.Identity" Version="1.12.0" />`},
	}
	for _, c := range cases {
		out, m := run(t, files, projects, "Directory.Packages.props", c.to, false, c.id)
		if m != nil || !strings.Contains(out[c.file], c.want) {
			t.Errorf("%s: %+v\n%s", c.id, m, out[c.file])
		}
	}
	for id, want := range map[string]string{"Range.Lib": "expression or a range", "Missing.Prop": "$(Nope) is not defined", "Not.There": "not referenced"} {
		if _, m := run(t, files, projects, "Directory.Packages.props", "1", false, id); m == nil || !strings.Contains(m.Reason, want) {
			t.Errorf("%s: %+v", id, m)
		}
	}
	onlyProject := []File{{Path: "App.csproj", Data: []byte(csproj)}}
	if _, m := run(t, onlyProject, projects, "", "1", false, "Azure.Identity"); m == nil || !strings.Contains(m.Reason, "managed outside") {
		t.Errorf("versionless without CPM file: %+v", m)
	}
}

func TestPinNuGet(t *testing.T) {
	files := []File{{Path: "App.csproj", Data: []byte(csproj)}, {Path: "Directory.Packages.props", Data: []byte(props)}}
	out, m := run(t, files, []string{"App.csproj"}, "Directory.Packages.props", "4.61.3", true, "Microsoft.Identity.Client")
	if m != nil {
		t.Fatal(m)
	}
	want := "    <PackageVersion Include=\"Azure.Identity\" Version=\"1.10.0\" />\n    <!-- Deepward: pinned transitive dependency: GHSA-1 - fix -->\n    <PackageVersion Include=\"Microsoft.Identity.Client\" Version=\"4.61.3\" />\n  </ItemGroup>"
	if !strings.Contains(out["Directory.Packages.props"], want) || out["App.csproj"] != csproj {
		t.Errorf("CPM transitive pinning:\n%s", out["Directory.Packages.props"])
	}
	noPinning := strings.Replace(props, "<CentralPackageTransitivePinningEnabled>true", "<CentralPackageTransitivePinningEnabled>false", 1)
	files[1].Data = []byte(noPinning)
	out, _ = run(t, files, []string{"App.csproj"}, "Directory.Packages.props", "4.61.3", true, "Microsoft.Identity.Client")
	if !strings.Contains(out["App.csproj"], `<PackageReference Include="Microsoft.Identity.Client" />`) ||
		!strings.Contains(out["Directory.Packages.props"], `Include="Microsoft.Identity.Client" Version="4.61.3"`) {
		t.Errorf("CPM without transitive pinning:\n%s\n%s", out["App.csproj"], out["Directory.Packages.props"])
	}
	if _, m := run(t, files, nil, "Directory.Packages.props", "1", true, "X"); m == nil {
		t.Error("no project file for the reference")
	}
	plain := strings.ReplaceAll("<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <TargetFramework>net8.0</TargetFramework>\n  </PropertyGroup>\n</Project>\n", "\n", "\r\n")
	pf := []File{{Path: "B.csproj", Data: []byte(plain)}}
	out, _ = run(t, pf, []string{"B.csproj"}, "", "4.61.3", true, "Microsoft.Identity.Client")
	if !strings.Contains(out["B.csproj"], "  <ItemGroup>\r\n    <!-- Deepward") || !strings.HasSuffix(out["B.csproj"], "  </ItemGroup>\r\n</Project>\r\n") {
		t.Errorf("new ItemGroup with CRLF:\n%q", out["B.csproj"])
	}
	if _, m := run(t, pf, nil, "", "1", true, "X"); m == nil {
		t.Error("no project file")
	}
	out, _ = run(t, files, []string{"App.csproj"}, "Directory.Packages.props", "1.13.0", true, "Azure.Identity")
	if !strings.Contains(out["Directory.Packages.props"], `Include="Azure.Identity" Version="1.13.0"`) {
		t.Error("pinning an already managed package edits its version")
	}
}

func TestScanErrorsAndLimits(t *testing.T) {
	if _, m := Plan([]File{{Path: "x.csproj", Data: []byte("<Project><a></Project")}}, nil, "", sdk.PackageChange{}, "X"); m == nil {
		t.Error("invalid XML")
	}
	loop := `<Project><PropertyGroup><A>$(B)</A><B>$(A)</B></PropertyGroup><ItemGroup><PackageReference Include="X" Version="$(A)" /></ItemGroup></Project>`
	if _, m := Plan([]File{{Path: "x.csproj", Data: []byte(loop)}}, nil, "", sdk.PackageChange{To: "1"}, "X"); m == nil || !strings.Contains(m.Reason, "nested") {
		t.Errorf("cycle: %+v", m)
	}
	cpmRange := `<Project><ItemGroup><PackageVersion Include="X" Version="[1,2)" /></ItemGroup></Project>`
	if _, m := Plan([]File{{Path: "Directory.Packages.props", Data: []byte(cpmRange)}}, nil, "", sdk.PackageChange{To: "1"}, "X"); m == nil {
		t.Error("range in PackageVersion")
	}
	if lineStart([]byte("ab"), 99) != 0 {
		t.Error("lineStart clamp")
	}
}

func TestPinTargets(t *testing.T) {
	plain := "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <ItemGroup>\n    <PackageReference Include=\"Some.Lib\" Version=\"1.0.0\" />\n  </ItemGroup>\n</Project>\n"
	pin := func(files []File, projects []string, props string, targets []string) map[string]string {
		t.Helper()
		edits, m := Plan(files, projects, props, sdk.PackageChange{To: "8.0.5", Pin: true, Targets: targets}, "System.Text.Json")
		if m != nil {
			t.Fatal(m)
		}
		out := map[string]string{}
		for _, f := range files {
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
	mods := func() []File {
		return []File{{Path: "Api/Api.csproj", Data: []byte(plain)}, {Path: "Core/Core.csproj", Data: []byte(plain)}, {Path: "Web/Web.csproj", Data: []byte(plain)}}
	}
	projects := []string{"Api/Api.csproj", "Core/Core.csproj", "Web/Web.csproj"}
	// No CPM: a versioned reference in each entry module only.
	out := pin(mods(), projects, "", []string{"Core/Core.csproj", "Elsewhere/X.csproj"})
	if !strings.Contains(out["Core/Core.csproj"], `<PackageReference Include="System.Text.Json" Version="8.0.5" />`) || out["Api/Api.csproj"] != plain || out["Web/Web.csproj"] != plain {
		t.Errorf("pin in Core only:\n%v", out)
	}
	// No usable target: the first project file, as before.
	out = pin(mods(), projects, "", nil)
	if !strings.Contains(out["Api/Api.csproj"], "System.Text.Json") || out["Core/Core.csproj"] != plain {
		t.Errorf("fallback to the first project file:\n%v", out)
	}
	// CPM without transitive pinning: PackageVersion plus version-less references in each target.
	noPinning := strings.Replace(props, "<CentralPackageTransitivePinningEnabled>true", "<CentralPackageTransitivePinningEnabled>false", 1)
	files := append(mods(), File{Path: "Directory.Packages.props", Data: []byte(noPinning)})
	out = pin(files, projects, "Directory.Packages.props", []string{"Core/Core.csproj", "Web/Web.csproj"})
	if !strings.Contains(out["Directory.Packages.props"], `<PackageVersion Include="System.Text.Json" Version="8.0.5" />`) ||
		!strings.Contains(out["Core/Core.csproj"], `<PackageReference Include="System.Text.Json" />`) ||
		!strings.Contains(out["Web/Web.csproj"], `<PackageReference Include="System.Text.Json" />`) || out["Api/Api.csproj"] != plain {
		t.Errorf("CPM without transitive pinning:\n%v", out)
	}
	// CPM with transitive pinning: the props file only.
	files = append(mods(), File{Path: "Directory.Packages.props", Data: []byte(props)})
	out = pin(files, projects, "Directory.Packages.props", []string{"Core/Core.csproj"})
	if out["Core/Core.csproj"] != plain || !strings.Contains(out["Directory.Packages.props"], `Include="System.Text.Json" Version="8.0.5"`) {
		t.Errorf("central transitive pinning:\n%v", out)
	}
}
