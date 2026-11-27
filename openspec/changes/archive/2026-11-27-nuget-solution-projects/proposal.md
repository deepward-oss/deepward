# Proposal

## Why

A .NET solution is split today into one Deepward project per directory with project files. A solution with thirty `.csproj` files becomes thirty projects: thirty `dotnet restore` runs that restore the same referenced projects again and again, the same vulnerable package reported thirty times, and with Central Package Management thirty fixes that edit the same `Directory.Packages.props` on thirty branches, so the change requests conflict with each other. The module model from `add-module-model` lets a whole solution be one project with its `.csproj` files as modules.

## What Changes

- **BREAKING** A NuGet project is now bounded by, in this order: the nearest `Directory.Packages.props` above a project file (the Central Package Management root), else the nearest `.sln` or `.slnx` above it that lists it, else the project file's directory as today. Project IDs, and therefore change request branches, change for solutions and CPM repositories.
- The project files of a NuGet project are its modules: graphs get one module node per `.csproj`/`.fsproj`/`.vbproj` and edges for `ProjectReference`s, and each package records the modules that resolve it.
- The build tool runs once per project: `dotnet restore` of the solution when the project has one, otherwise of the project files no other member references; then every member's `project.assets.json` is read.
- Fixes of a NuGet project are one change request per package for the whole solution. A pin of a transitive package goes to Central Package Management when it pins transitively, otherwise to the modules through which the package enters the project, so referencing projects inherit it.
- Open change requests of NuGet projects that became modules of a larger project are closed with a comment naming the project that replaces them, instead of staying open forever.
- `pkg/sdk`: `PackageChange.Targets` (manifests a change should be applied to) and `Graph.EntryModules` (modules through which a package enters), both additive.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `dependency-graph`: NuGet project boundaries (CPM root, solution, directory), NuGet project files as modules, one restore per project.
- `change-requests`: NuGet pins go to the entering modules; requests of projects merged into another project are closed.

## Impact

- `plugins/ecosystem/nuget` (`Detect`, `Produce`, `Realize`, `domain/assets.go` for project references and module mapping, `domain/edit.go` for pins in target files, new solution parsing for `.sln` and `.slnx`).
- `pkg/sdk` (`PackageChange.Targets`), `pkg/sdk/deps` (`Graph.EntryModules`), `plugins/analyzer/depvuln` (targets of pins), `internal/engine/publish.go` (closing requests of merged projects).
- Users: NuGet project IDs and branches change once; per-project settings under `projects.<dir>` written for a `.csproj` directory must move to the new project directory; existing change requests are closed and reopened under the new branches on the first run.
- Docs: `docs/ecosystems/nuget.md`, `docs/publishing.md`, `CHANGELOG.md` (breaking).
- No new dependencies.
