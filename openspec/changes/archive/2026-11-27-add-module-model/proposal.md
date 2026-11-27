# Proposal

## Why

Multi-module builds are flattened today. A Maven reactor or an sbt build is one project whose modules dissolve into the project root: only direct dependencies remember which modules declare them, paths start at a dependency instead of a module, scopes are merged across modules (a test-only library shows as `compile`), and the "declared in" location is the first manifest line that mentions the artifact, in any module. On real reactors (for example nine modules in one repository) users cannot tell which module brings a vulnerable package or where to change it. This change introduces modules as a common, ecosystem-neutral concept and applies it to Maven and sbt first; NuGet follows in `nuget-solution-projects`, other ecosystems later.

## What Changes

- The dependency graph can contain **module nodes**: one per member of a multi-module build (Maven module, sbt subproject). The project root points at its modules, modules point at their dependencies and at the modules they depend on. Package nodes keep their purls.
- A package node records **its scope in each module** that reaches it; the existing node scope stays the widest of them.
- A package is **direct** when a module (or the root, for graphs without modules) declares it; direct ancestors are found below modules.
- **Dependency paths start at a module** (`wr-history-api → kafka-clients → lz4-java`) when the graph has modules.
- Vulnerability and outdated findings list the **affected modules** (name, scope, whether the module declares the package directly), and their file location is the manifest of the module that declares the package, closest to it.
- The HTML report shows modules: module names in paths, the affected modules with their scopes in each row's details, and module names in the search; the JSON result carries the same data.
- Change request descriptions list the modules each change affects.
- The Maven plugin turns reactor modules into module nodes, with per-module scopes from each module's tree and edges between modules; the sbt plugin does the same for subprojects.
- Ecosystems that do not report modules keep today's graphs and findings unchanged.
- The node attributes `modules` (Maven) and `projects` (sbt) on direct dependencies are replaced by the per-module data.
- `pkg/sdk` gains the module concept (minor SDK version bump; existing plugins keep working).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `dependency-graph`: graphs may contain module nodes with per-module scopes; paths and direct dependencies are relative to modules; Maven modules and sbt subprojects become module nodes.
- `vulnerability-analysis`: findings list affected modules and point at the declaring module's manifest.
- `update-discovery`: outdated findings list the modules that declare the dependency.
- `scan-report`: the HTML and JSON reports show modules.
- `change-requests`: change request descriptions list the affected modules.

## Impact

- `pkg/sdk/deps` (`Node`, `NewGraph`, `Packages`, `DirectAncestors`, `Paths`, new module helpers) and `pkg/sdk` (`PackageLocation`); `SDKVersion` 0.3.0.
- Plugins: `ecosystem/maven` (`domain/tree.go`), `ecosystem/sbt` (`domain/report.go`), `analyzer/depvuln`, `analyzer/outdated`, `reporter/html`; `internal/engine` (`publish.go` body).
- Golden files of the JSON, SARIF and HTML reporters; `schema/deepward.schema.json` is unaffected (configuration does not change).
- JSON result: additive fields (`kind` and `modules` on graph nodes, `modules` on package locations); module IDs appear in `paths`. Finding fingerprints do not change, so suppressions keep working.
- No new dependencies.
