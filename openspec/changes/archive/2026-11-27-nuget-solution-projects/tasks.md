# Tasks

## 1. SDK hints for module-aware fixes

- [x] 1.1 Add `PackageChange.Targets` (`pkg/sdk`) and `Graph.EntryModules` (`pkg/sdk/deps/graph.go`: walk parents from a package, stop at modules, return them sorted); verify with graph tests that a package brought by `Core` and reached by `Web` and `Api` through `Core` has entry module `Core`, and that a graph without modules returns none
- [x] 1.2 Set `Targets` of pin remediations in `analyzer/depvuln` to the manifests of the entry modules, and describe `Targets` for plugin authors in `docs/plugins.md`; verify with a depvuln test that the pin of a package entering through `Core` targets `Core/Core.csproj` and that pins without modules have no targets

## 2. NuGet boundaries and restore

- [x] 2.1 Add pure parsers to `plugins/ecosystem/nuget/domain` for `.sln` and `.slnx` project paths and for `ProjectReference` paths of a project file (normalizing backslashes, skipping solution folders); verify with unit tests on sample files, including a solution folder and a reference with `..\`
- [x] 2.2 Rework `Detect` to choose each project file's boundary (nearest `Directory.Packages.props`, else nearest listing solution with ties by path, else its directory), with manifests and `projectFiles`/`solutions` attributes; verify with `TestDetect` cases for a CPM root, a solution without CPM, a project file in no solution and a project file in two solutions
- [x] 2.3 Rework `Produce` to restore the solution once when all its projects are members and otherwise each member no other member references, then read every member's assets; verify with a `TestProduce` case using the fake toolchain that a thirty-project solution runs one restore and a CPM root without a solution restores only unreferenced members

- [x] 2.4 Restore a member on its own when the solution restore left it without an assets file (projects outside the solution's active configuration), and report a missing assets file only after that; verify with a `TestProduce` case where the solution restore writes assets for one of two members, so the other is restored alone and both become modules, and with a failing fallback restore

## 3. NuGet modules in graphs and fixes

- [x] 3.1 Extend `domain.ParseAssets` with project libraries' paths and rework `domain.BuildGraph` to emit module nodes per member, module-to-module edges for project references inside the boundary, and `Modules` per owner; verify with an assets fixture of `Web` referencing `Core` that `Newtonsoft.Json` is reached by both, `Web → Core` is an edge, and a single project file keeps a flat graph
- [x] 3.2 Make `Realize`/`domain.Plan` apply pins to `Targets` (CPM with transitive pinning: `PackageVersion` only; CPM without it: `PackageVersion` plus version-less `PackageReference` per target; no CPM: `PackageReference` with version per target; fallback to the first project file); verify with realize tests for all three cases and the fallback
- [x] 3.3 Rewrite `docs/ecosystems/nuget.md` detection, graph and fix sections for boundaries, modules, one restore and pin targets, and verify `go test ./test/docs` passes

## 4. Change requests of merged projects

- [x] 4.1 Close open bot requests of projects that were not scanned but whose directory lies inside a scanned project of the same ecosystem, with a comment naming the new project, in `internal/engine/publish.go`; verify with publish tests that a request of `nuget:src/Web` is closed when `src/Web/Web.csproj` is a manifest of `nuget:.`, and that a request of a project nobody contains stays open
- [x] 4.2 Mention the closing of merged projects in `docs/publishing.md` and add the breaking NuGet change with migration notes (new project IDs and branches, `projects.<dir>` settings, reopened SARIF alerts) to `CHANGELOG.md` under `[Unreleased]`; verify `go test ./test/docs` passes

## 5. Integration check

- [x] 5.1 Run `golangci-lint run ./...`, `go test ./...` and the coverage check, and verify all pass
- [x] 5.2 Run `deepward scan` on a solution fixture with two referencing projects under CPM using a fake `dotnet`, and verify one project `nuget:.` with both modules, one restore invocation, and findings listing both modules

## Workflow follow-up

- Archive the change with `/opsx:archive` after review.
