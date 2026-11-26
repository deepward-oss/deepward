package nuget

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

func TestRealize(t *testing.T) {
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{
		"client/App.csproj":        {Data: []byte(`<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Newtonsoft.Json" Version="12.0.3" /><PackageReference Include="Azure.Identity" /></ItemGroup></Project>`)},
		"Directory.Packages.props": {Data: []byte("<Project>\n  <PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>\n  <ItemGroup>\n    <PackageVersion Include=\"Azure.Identity\" Version=\"1.10.0\" />\n  </ItemGroup>\n</Project>\n")},
		"client/NuGet.Config":      {Data: []byte("<configuration/>")},
	}}
	project := sdk.Project{ID: "nuget:client", Ecosystem: ID, Dir: "client",
		Manifests: []string{"client/App.csproj", "client/NuGet.Config", "Directory.Packages.props"}}
	res, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: project, Workspace: ws, Changes: []sdk.PackageChange{
		{PURL: "pkg:nuget/newtonsoft.json", To: "13.0.1"},
		{PURL: "pkg:nuget/azure.identity", To: "1.12.0"},
		{PURL: "pkg:nuget/microsoft.identity.client", To: "4.61.3", Pin: true},
		{PURL: "pkg:nuget/not.used", To: "1"},
		{PURL: "garbage", To: "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, e := range res.Edits {
		paths[e.Path]++
	}
	if paths["client/App.csproj"] != 2 || paths["Directory.Packages.props"] != 2 || len(res.Manual) != 2 {
		t.Errorf("edits %v manual %+v", paths, res.Manual)
	}
	if _, err := New().Realize(context.Background(), sdk.RealizeRequest{Project: sdk.Project{Manifests: []string{"missing.csproj"}}, Workspace: ws}); err == nil {
		t.Error("missing file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Realize(ctx, sdk.RealizeRequest{Project: project, Workspace: ws}); err == nil {
		t.Error("cancelled")
	}
}
