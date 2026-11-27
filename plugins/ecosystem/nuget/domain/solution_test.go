package domain

import (
	"reflect"
	"testing"
)

func TestSolutionProjects(t *testing.T) {
	sln := "\r\nMicrosoft Visual Studio Solution File, Format Version 12.00\r\n" +
		"Project(\"{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}\") = \"Web\", \"src\\Web\\Web.csproj\", \"{11111111-1111-1111-1111-111111111111}\"\r\nEndProject\r\n" +
		"Project(\"{2150E333-8FDC-42A3-9474-1A3956D46DE8}\") = \"src\", \"src\", \"{22222222-2222-2222-2222-222222222222}\"\r\nEndProject\r\n" +
		"Project(\"{F2A71F9B-5D33-465A-A702-920D77279786}\") = \"Lib\", \"..\\shared\\Lib.fsproj\", \"{33333333-3333-3333-3333-333333333333}\"\r\nEndProject\r\n"
	if got := SolutionProjects([]byte(sln)); !reflect.DeepEqual(got, []string{"src/Web/Web.csproj", "../shared/Lib.fsproj"}) {
		t.Errorf("sln projects (solution folder skipped): %v", got)
	}
	slnx := `<Solution><Folder Name="/src/"><Project Path="src/Core/Core.csproj" /><Project Path="docs/readme.md" /></Folder>
<Project Path="tests\Core.Tests\Core.Tests.csproj"></Project></Solution>`
	got, err := SlnxProjects([]byte(slnx))
	if err != nil || !reflect.DeepEqual(got, []string{"src/Core/Core.csproj", "tests/Core.Tests/Core.Tests.csproj"}) {
		t.Errorf("slnx projects: %v %v", got, err)
	}
	if _, err := SlnxProjects([]byte("<Solution><Project")); err == nil {
		t.Error("broken slnx")
	}
}

func TestProjectReferences(t *testing.T) {
	csproj := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="..\Core\Core.csproj" />
    <ProjectReference Include="../Shared/Shared.csproj"><PrivateAssets>all</PrivateAssets></ProjectReference>
    <PackageReference Include="Newtonsoft.Json" Version="12.0.1" />
  </ItemGroup>
</Project>`
	got, err := ProjectReferences([]byte(csproj))
	if err != nil || !reflect.DeepEqual(got, []string{"../Core/Core.csproj", "../Shared/Shared.csproj"}) {
		t.Errorf("project references: %v %v", got, err)
	}
	if p, ok := Resolve("src/Web", "../Core/Core.csproj"); !ok || p != "src/Core/Core.csproj" {
		t.Errorf("resolve: %q %v", p, ok)
	}
	if _, ok := Resolve(".", "../outside/X.csproj"); ok {
		t.Error("paths outside the workspace are rejected")
	}
	if !IsProjectFile("A/B.VBPROJ") || IsProjectFile("A/B.props") {
		t.Error("IsProjectFile")
	}
}
