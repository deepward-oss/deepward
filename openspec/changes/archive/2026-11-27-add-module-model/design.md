# Design

## Context

See proposal.md for the motivation. Today `deps.Graph` has one root and package nodes identified by purls (`NewGraph` rejects anything else), `Direct` means "child of the root", `DirectAncestors` stops at the root's children and `Paths` start at the root's first child. The Maven plugin (`domain.BuildGraph` in `tree.go`) reads one dependency tree per reactor module, drops module nodes and their inlined subtrees, links module dependencies to the root and keeps only an attribute `modules` on direct nodes; scopes are merged to the widest. The sbt plugin (`domain.BuildGraph` in `report.go`) does the same with its per-subproject reports and an attribute `projects`. Consumers of the graph are `analyzer/depvuln` (`Packages`, `Direct`, `DirectAncestors`, `Paths`, `manifestLocation`), `analyzer/outdated` (`Packages`, `Direct`, `manifestLocation`), the HTML reporter (through `PackageLocation`), `ScanResult` summary counts (`PackageLocation.Direct`) and the CycloneDX import (input only). Finding fingerprints use the analyzer, rule, project and the package purl without version, never paths or files.

## Goals / Non-Goals

**Goals:**
- One module concept in `pkg/sdk`, usable by every ecosystem, opt-in per plugin.
- Maven reactors and sbt builds report modules; every other ecosystem is unchanged.
- No change to finding identity, configuration or project IDs.

**Non-Goals:**
- New project boundaries (NuGet solutions, Go workspaces) — `nuget-solution-projects` and later changes.
- Module-aware fixes (editing only some modules, sbt pins); Maven edits already cover every POM that declares a dependency.
- Building a single module (`mvn -pl`) or per-module verification.
- Module filters in the report beyond text search.

## Decisions

**Modules are graph nodes, not annotations.** A module node has `Kind: "module"`, an ID `module:<key>` (Maven: the POM path, for example `module:wr-history-api/pom.xml`; sbt: the subproject ID), `Name` (artifactId or subproject ID) and, when it has its own manifest, `Attributes["manifest"]`. Edges go root → module, module → package, module → module. With nodes, the existing algorithms keep working: `Paths` start at a module because modules are the root's children, the modules that reach a package are a reverse reachability, different versions per module are different subtrees, and dependencies between modules are plain edges. Alternative: keep the flat graph and add a `modules` map to every node plus per-module root edges — rejected because paths, direct ancestors and module-to-module dependencies would each need their own bookkeeping.

**SDK surface.** `deps.Node` gains `Kind string` (`""` for packages, `deps.KindModule`) and `Modules map[string]string` (module ID → scope in that module). `NewGraph` accepts `module:` IDs only on module nodes; after normalizing it fills `Modules` for every package: modules found by one forward walk per module that does not cross other modules (modules are few, so O(modules × edges)), keeping the scopes the plugin gave and using the node scope for modules the plugin did not mention. What a module gets through a module it depends on comes from the plugin's per-module data, because the build tools already report it per module (Maven inlines sub-module subtrees, sbt update reports include inter-project libraries, NuGet assets include packages flowing through project references), and a plain walk across modules would wrongly hand test-only dependencies of one module to the modules that depend on it. Plugins compute the node scope as the widest per-module scope, as they already merge scopes today. `Direct` becomes "has a parent that is the root or a module". `Packages()` excludes module nodes; new `Modules()` lists them. `DirectAncestors` treats children of the root and of modules as direct and never returns a module. `sdk.PackageLocation` gains `Modules []sdk.ModuleRef` (`ID`, `Name`, `Scope`, `Direct`), and `SDKVersion` becomes 0.3.0. Everything is additive; a plugin that emits no module nodes gets exactly today's graph.

**Maven graph.** Only a reactor with more than one module gets module nodes; a single-module project keeps its flat graph, so plain projects do not gain a redundant first hop in every path. `Produce` already parses the POMs of the project (`project.Manifests`); a map from `groupId:artifactId` to POM path names each tree's module. For each module tree: root → module node; children walked as today, with `Modules[module] = widest scope seen in this tree`. A child that is a reactor module becomes an edge to that module node, and the walk continues into its inlined subtree only to record scopes for the current module (its edges equal those from the module's own tree and are deduplicated). The `modules` attribute is dropped.

**sbt graph.** Each `ProjectReport` (per subproject and Scala version) maps to `module:<project>`; a map from build module coordinates to subproject IDs turns callers that are build modules into module → package edges and build modules among a subproject's dependencies into module → module edges. Per-subproject scopes come from the configuration names as today. The `projects` attribute is dropped. Subprojects have no manifest of their own, so locations fall back to the build files.

**Findings.** `depvuln` and `outdated` build `PackageLocation.Modules` from `Node.Modules`, sorted by name; `Direct` per module means an edge from that module. `depvuln`'s location: take the closest direct ancestor (or the package when direct); among modules with an edge to it, pick the one on the shortest path, then the first by name; search its `manifest` attribute first for the artifact name, then fall back to the current search over all project manifests. `outdated` uses the first declaring module by name. Fingerprints are untouched.

**Report.** The HTML view maps module IDs in paths to names (from the location's modules) and renders them with a distinct style; the details list modules as `name (scope, direct)`; outdated rows show the declaring modules; module names join the search text. Rows without modules render exactly as now, so old JSON results still render. The JSON result gets the new fields through the existing types.

**Change request body.** `Body` adds the affected modules of each item, collected from the item's findings, as a `Modules` column of the change table when at least one item has modules.

## Risks / Trade-offs

- [Graph JSON gains nodes that are not packages] → They are marked `kind: "module"` and use the `module:` prefix; documented for consumers of the dependency-graph fact.
- [Paths in findings contain module IDs instead of purls for the first hop] → Documented; the HTML report resolves them to names, `ModuleRef` gives the mapping to other consumers, and SARIF does not include paths.
- [A plugin that emits module-to-module edges but no per-module data under-reports modules reached through other modules] → The SDK never guesses across modules; Maven and sbt provide the data, and the docs for plugin authors say so.
- [Plugins outside this repository that rely on `Direct` meaning "child of the root"] → Unchanged for graphs without modules, which is every graph such a plugin produces.
- [Dropping the `modules`/`projects` attributes] → They were informational; the same data is in `Node.Modules`. Mentioned in the changelog.
