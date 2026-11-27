# Design

## Context

See proposal.md for the motivation. `nuget.Detect` groups project files by directory and adds the shared files of the directory and its parents (`Directory.Packages.props`, `Directory.Build.props`, `NuGet.Config`, `global.json`, a local `packages.lock.json`); solution files are ignored. `Produce` runs `dotnet restore <project file>` for every project file of the directory and merges their `obj/project.assets.json` with `domain.BuildGraph`, linking packages of referenced projects to the root. `Realize` passes all project files and the first `Directory.Packages.props` to `domain.Plan`, whose `pin` inserts a `PackageReference` into `projectFiles[0]` (plus a `PackageVersion` under CPM). `add-module-model` provides module nodes (`deps.KindModule`, `Node.Modules`, `Graph.ModuleRefs`), and `Publish` closes a stale bot request only when its project was scanned. Project IDs are `nuget:<dir>` and feed branch names (`{project}`) and finding fingerprints.

## Goals / Non-Goals

**Goals:**
- One NuGet project per CPM root or solution, with project files as modules, restored once.
- One fix per package for the whole project; pins in the right project files.
- A one-time, self-cleaning migration of open change requests.

**Non-Goals:**
- Solution filters (`.slnf`), `packages.config` projects, `Directory.Build.targets` edits.
- Per-module verification or restoring a single module.
- Configurable boundaries (a setting to keep per-directory projects).

## Decisions

**Boundary rule.** `Detect` collects project files, solutions (`.sln`, `.slnx`) and `Directory.Packages.props` files in one walk. For each project file: the nearest `Directory.Packages.props` at or above it wins, because MSBuild applies exactly that file and it is where versions and pins live; else the nearest solution at or above it that lists it (ties: first by path); else its directory. The project directory is the boundary's directory, its manifests are the member project files, the boundary file, each member's `packages.lock.json`, and the shared files at or above the boundary directory. Attributes record `projectFiles` and `solutions`: the solutions at or below the project directory whose listed projects are all members, which are the ones restored. Alternative: solution first, then CPM — rejected because one CPM root with several solutions would split one version file across several projects, which is what causes conflicting change requests today.

**Solution parsing** lives in `nuget/domain` as pure functions: `.sln` lines `Project("{type}") = "Name", "rel\path.csproj", "{id}"` (backslashes normalized, entries without a project-file extension such as solution folders skipped) and `.slnx` `<Project Path="..."/>` elements; paths are relative to the solution. `ProjectReference Include` values of member project files are parsed the same way (relative to the project file) to find members that other members reference.

**One restore.** With a solution whose listed projects are all members, `dotnet restore <solution>` runs once. For members a restored solution does not list, `dotnet restore <project file>` runs for each member that no other member references; restoring a project file also restores what it references. Then every member's assets file is read (with the existing `ProjectAssetsFile` fallback for custom intermediate paths). A solution restore silently skips projects without a mapping for its active configuration (`ProjectConfigurationPlatforms`), so a member still without an assets file is restored on its own once, and only then is a missing assets file an error. Alternative: parse the solution's configuration sections to predict skipped projects — rejected because the restore itself is the source of truth and the check costs one `stat` per member.

**Modules in the graph.** `domain.BuildGraph` receives each assets file with its owner's repository path. With more than one member, each member is a module node `deps.ModuleID(<path>)` named by the file name without extension, with `Attributes["manifest"]` set to its path. In the owner's targets, a `project` library whose `libraries[key].path` (relative to the owner's directory) resolves to a member becomes a module → module edge; its listed dependencies are edges from that member's module (identical to the member's own assets, deduplicated). A `project` library outside the boundary is not a module: its packages hang off the owner as today. Every package in an owner's targets records `Modules[owner] = compile`, because NuGet's assets already include what flows through project references; target frameworks stay merged in the `targetFrameworks` attribute. With one member, the graph stays flat.

**Pin targets.** `sdk.PackageChange` gains `Targets []string`: manifests a change should be applied to, a hint that ecosystems may ignore (Maven pins in the root POM, sbt pins are manual). `deps.Graph.EntryModules(id)` walks parents from a package, stops at modules and returns the modules reached, sorted: the modules through which the package enters from outside. `depvuln` sets `Targets` of a pin to their manifests. `nuget.Plan`'s pin then: with CPM and transitive pinning, a `PackageVersion` only; with CPM without it, a `PackageVersion` plus a version-less `PackageReference` in each target; without CPM, a `PackageReference` with version in each target. Targets outside the project's files are dropped; with none left, the first project file is used as today.

**Closing requests of merged projects.** In `Publish`, an open bot request whose marker names a project that was not scanned is closed when some scanned project of the same ecosystem lists a manifest under that project's directory (or the directory is `.` of a now larger project), with the comment "Deepward closed this request: project `<old>` is now part of `<new>`." Any other missing project keeps its requests, as today, so a failed or temporarily excluded project never loses them.

## Risks / Trade-offs

- [Project IDs change for NuGet, so branches, fingerprints and `projects.<dir>` settings keyed by a project-file directory change] → Marked **BREAKING** in the changelog with migration notes; old change requests are closed automatically and replaced by one request per package; SARIF alerts keyed by fingerprints are reopened once by code scanning.
- [A solution can list project files outside its directory] → They belong to the solution's project only when no nearer CPM root claims them; paths are normalized and must stay inside the workspace, otherwise they are ignored.
- [`dotnet restore <solution>` fails for one broken project and fails the whole project] → Same failure handling as today (project marked failed, tool output kept); previously only that directory failed. Documented.
- [Entry modules for pins may be several] → Each gets a `PackageReference`; references flow to dependents, so the pin is redundant but harmless where a dependent is also an entry module.
