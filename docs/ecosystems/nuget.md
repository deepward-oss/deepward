# NuGet (.NET)

The `nuget` ecosystem plugin handles SDK-style .NET projects (`*.csproj`,
`*.fsproj`, `*.vbproj`).

## Detection

A project is the unit Deepward builds, reports and fixes. Each project file
belongs to exactly one project, chosen in this order:

1. the directory of the nearest `Directory.Packages.props` at or above it
   (the Central Package Management root, where versions and pins live);
2. else the directory of the nearest solution (`.sln` or `.slnx`) at or
   above it that lists it; when two are equally near, the first by path;
3. else its own directory.

A repository with `Directory.Packages.props` at the root and thirty project
files is therefore one project, `nuget:.`. Its manifests are the project
files, the solution or `Directory.Packages.props` that bounds it, a
`packages.lock.json` next to each project file, and the shared files found
in the project directory or its parents: `Directory.Packages.props`,
`Directory.Build.props`, `NuGet.Config` and `global.json`.

## Dependency graph

```text
dotnet restore <solution or project file> [--configfile <nuget.config>]
```

Restore runs once per project: on each solution at or below the project
directory whose projects all belong to the project, then on every other
project file that no project file of the project references (restoring a
project file also restores what it references). A solution restore skips
projects that its active configuration does not build; such a project file
is then restored on its own.

The graph comes from each project file's `obj/project.assets.json`, NuGet's
fully resolved graph for each target framework. With several target
frameworks, packages from all of them are merged, and each node records its
`targetFrameworks`. If the intermediate directory is customized, Deepward
asks MSBuild for `ProjectAssetsFile` (requires SDK 8 or later).

### Modules

In a project with more than one project file, every project file is a
**module**, named by its file name without extension:

- a `ProjectReference` between project files of the project is a step in the
  path (`Web → Core → Newtonsoft.Json`), not a package;
- every package records each module whose assets contain it, including the
  packages that flow in through project references;
- findings list the affected modules and point at the referencing line in
  the module's own project file.

A project reference to a project file of another project is not a module:
its packages count as packages of the referencing project file. A project
with one project file has no modules.

`dotnet list package --include-transitive` is not used, because its text
output changes between SDK versions.

Requirements: .NET SDK 6 or later.

## Fixes

A package is fixed once for the whole project, in one change request:

- a declared version is changed where it is declared: a `PackageReference`
  version (attribute, child element or `$(Property)`) in every project file
  that has one, or the `PackageVersion` in `Directory.Packages.props`;
- a pin of a transitive package is a `PackageVersion` in
  `Directory.Packages.props` when `CentralPackageTransitivePinningEnabled`
  is `true`; otherwise it adds a `PackageReference` to each module through
  which the package enters the project, so the project files that reference
  that module inherit the pin.

## Migrating from per-directory projects

Before this layout, every directory with project files was its own project
(`nuget:src/Web`). On the first run with the new layout, project IDs and
change request branches change: open requests of the old projects are closed
with a comment naming the project that replaces them, and new requests are
opened per package for the whole project. Settings under `projects.<dir>`
for a project-file directory must move to the new project directory.

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
