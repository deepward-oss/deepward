# NuGet (.NET)

The `nuget` ecosystem plugin handles SDK-style .NET projects (`*.csproj`,
`*.fsproj`, `*.vbproj`).

## Detection

Each directory with project files is one project. Its manifests are the
project files, a `packages.lock.json` next to them, and the shared files found
in the directory or its parents: `Directory.Packages.props` (Central Package
Management), `Directory.Build.props`, `NuGet.Config` and `global.json`.
Solution files are not projects themselves: every project they reference is
detected through its own directory.

## Dependency graph

```text
dotnet restore <project file> [--configfile <nuget.config>]
```

The graph comes from `obj/project.assets.json`, NuGet's fully resolved graph
for each target framework. With several target frameworks, packages from all
of them are merged, and each node records its `targetFrameworks`. Project
references are part of the build: their packages become direct dependencies
of the project. If the intermediate directory is customized, Deepward asks
MSBuild for `ProjectAssetsFile` (requires SDK 8 or later).

`dotnet list package --include-transitive` is not used, because its text
output changes between SDK versions.

Requirements: .NET SDK 6 or later.

## Feeds and authentication

Restore uses your normal `nuget.config`. To pass another one, set
`plugins.nuget.configFile`. Version discovery reads the NuGet v3 flat
container of the feeds below, in order:

```yaml
plugins:
  nuget:
    configFile: ci/nuget.config
    feeds:
      - id: corp
        url: https://nexus.acme.local/repository/nuget-group/index.json
        usernameEnv: NEXUS_USER
        passwordEnv: NEXUS_PASSWORD
      - id: nuget.org
        url: https://api.nuget.org/v3/index.json
```

Credentials come from environment variables. Azure Artifacts and GitHub
Packages accept a personal access token: set `tokenEnv`, or use
`usernameEnv` + `passwordEnv` with the token as the password.

## Dotnet tools

Restore local tools before graphs are built, for example to run the
CycloneDX dotnet tool as an integration:

```yaml
ecosystems:
  nuget:
    setup:
      - tool: dotnet
        args: [tool, restore]
```
