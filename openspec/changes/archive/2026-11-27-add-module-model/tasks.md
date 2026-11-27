# Tasks

## 1. Module model in the SDK

- [x] 1.1 Add `Node.Kind` (`deps.KindModule`), `Node.Modules`, the `module:` ID helper and module validation in `NewGraph` (`pkg/sdk/deps/graph.go`); verify with graph tests that a `module:` ID on a package node is rejected and a module node is accepted
- [x] 1.2 Fill `Modules` for package nodes in `NewGraph` (one forward walk per module; plugin scopes kept, node scope for unmentioned modules, node scope = widest per-module scope), derive `Direct` from root or module parents, exclude modules from `Packages()` and add `Modules()`; verify with tests for every `dependency-graph` scenario (shared dependency, module-to-module edge, test library in every module, different scopes and versions per module, package reached through another module) and that a graph without modules is unchanged
- [x] 1.3 Make `DirectAncestors` treat children of modules as direct and never return a module, and keep `Paths` starting at the module; verify with tests that `lz4-java` has the path `api → kafka-clients → lz4-java` and `kafka-clients` as its direct ancestor
- [x] 1.4 Add `sdk.ModuleRef` and `PackageLocation.Modules`, set `SDKVersion` to 0.3.0, regenerate the JSON reporter golden file, and verify `go test ./pkg/... ./plugins/reporter/json` passes
- [x] 1.5 Describe module nodes, `Node.Modules` and `PackageLocation.Modules` for plugin authors in `docs/plugins.md`, and verify `go test ./test/docs` passes

## 2. Maven modules

- [x] 2.1 Rework `BuildGraph` in `plugins/ecosystem/maven/domain/tree.go` to emit module nodes named by artifactId with POM-path IDs, module-to-module edges and per-module scopes from each module's tree, dropping the `modules` attribute; pass the coordinates-to-POM map from `Produce`; verify with `tree-multi.json` tests that every reactor module is a module node, inter-module dependencies are edges and a test library is `test` in each module
- [x] 2.2 Document modules in `docs/ecosystems/maven.md` (what a module node is, paths from modules, per-module scopes) and verify `go test ./test/docs` passes

## 3. sbt subprojects

- [x] 3.1 Rework `BuildGraph` in `plugins/ecosystem/sbt/domain/report.go` to emit one module node per subproject, module-to-module edges from `dependsOn` and per-subproject scopes, dropping the `projects` attribute; verify with `testdata/graph.jsonl` tests that `root` depends on `core` and that a dependency of `core` is reached by both
- [x] 3.2 Document subprojects as modules in `docs/ecosystems/sbt.md` and verify `go test ./test/docs` passes

## 4. Findings

- [x] 4.1 Fill `PackageLocation.Modules` in `analyzer/depvuln` and choose the location from the declaring module's manifest (closest direct ancestor, shortest path, then name, falling back to all manifests); verify with depvuln tests that `lz4-java` lists both modules and is located at `wr-history-api/pom.xml` on the `kafka-clients` line, and that fingerprints equal those of the same graph without modules
- [x] 4.2 Fill `PackageLocation.Modules` in `analyzer/outdated` with the declaring modules and locate the finding in the first declaring module's manifest; verify with outdated tests for a dependency declared in several modules

## 5. Report and change requests

- [x] 5.1 Show modules in the HTML report (module names in paths with their own style, a modules line in vulnerability details, declaring modules in outdated rows, module names in the search) and regenerate the HTML golden file; verify with HTML tests for a vulnerability reached from two modules, the search text, and an old result without modules rendering unchanged
- [x] 5.2 Add the affected modules of each item to change request descriptions in `internal/engine/publish.go` (a `Modules` column when any item has modules); verify with a `Body` test for a pin affecting two modules and that bodies without modules are unchanged
- [x] 5.3 Add the module model to `CHANGELOG.md` under `[Unreleased]` (including the dropped `modules`/`projects` attributes and SDK 0.3.0) and verify by reading it

## 6. Integration check

- [x] 6.1 Run `golangci-lint run ./...`, `go test ./...` and the coverage check, and verify all pass
- [x] 6.2 Run `deepward scan` on `testdata/projects/maven-multi` with the fake `mvn` and verify in the JSON and HTML reports that findings list the `core` and `web` modules and paths start with module names

## Workflow follow-up

- Archive the change with `/opsx:archive` after review.
