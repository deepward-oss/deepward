# dependency-graph Specification

## Purpose
Detect sbt, Maven and NuGet projects in a repository working copy and build a complete, normalized dependency graph (direct and transitive) using each ecosystem's native build tool.

## Requirements

### Requirement: Project detection
The system SHALL recursively detect projects of supported ecosystems under the scan root by marker files: `build.sbt` (sbt), `pom.xml` (Maven), `*.csproj`, `*.fsproj`, `*.sln`, `Directory.Packages.props` (NuGet). Directories excluded in configuration, plus `target`, `bin`, `obj`, `node_modules` and `.git`, SHALL be skipped.

#### Scenario: Monorepo with three ecosystems
- **WHEN** the root contains `backend/build.sbt`, `services/api/pom.xml` and `client/App.csproj`
- **THEN** the system reports three projects with ecosystems sbt, maven and nuget and their relative paths

#### Scenario: Nested Maven modules
- **WHEN** the root `pom.xml` declares modules `core` and `web`, each with its own `pom.xml`
- **THEN** the system treats them as one Maven project rooted at the root `pom.xml` and does not invoke the build tool per module

#### Scenario: Excluded directory
- **WHEN** configuration excludes `examples/` and that directory contains a `pom.xml`
- **THEN** that project is not reported

### Requirement: Graph from the native build tool
The system SHALL obtain transitive dependencies by invoking the ecosystem's build tool (sbt, mvn, dotnet) rather than parsing manifests itself. Resolved versions in the graph MUST match the versions the build tool selects during conflict resolution.

#### Scenario: Maven version conflict
- **WHEN** two direct dependencies pull `com.fasterxml.jackson.core:jackson-databind` 2.15.0 and 2.17.1 and Maven selects 2.15.0 (nearest wins)
- **THEN** the graph contains a single `jackson-databind` node with version 2.15.0

#### Scenario: Build tool not installed
- **WHEN** an sbt project is found but `sbt` is not on `PATH` and no path is configured
- **THEN** the project is marked skipped with reason `tool-not-found`, other projects are still scanned, and the report contains a warning

#### Scenario: Build failure
- **WHEN** the build tool exits non-zero while producing the graph
- **THEN** the project is marked failed, the last 200 lines of tool output are kept in the results, and scanning of other projects continues

### Requirement: Normalized graph model
Every graph node SHALL be identified by a purl (`pkg:maven/...`, `pkg:nuget/...`) and SHALL carry its ecosystem, resolved version, scope, a direct-dependency flag, and the paths from the project root to the node.

#### Scenario: Scala artifact with cross-version suffix
- **WHEN** a Scala 2.13 sbt project depends on `"org.typelevel" %% "cats-core" % "2.10.0"`
- **THEN** the node purl is `pkg:maven/org.typelevel/cats-core_2.13@2.10.0`, and the base name `cats-core` and suffix `_2.13` are kept as separate attributes

#### Scenario: Path to a transitive dependency
- **WHEN** `app` depends on `A`, `A` on `B`, and `B` on `log4j-core`
- **THEN** the `log4j-core` node has the path `app → A → B → log4j-core` and `A` as its direct ancestor

#### Scenario: Test-scoped dependency
- **WHEN** a dependency is declared in Maven scope `test` or sbt configuration `Test`
- **THEN** the node has scope `test` and can be filtered out by configuration

### Requirement: Private registries during resolution
The system SHALL pass the project's private repository settings (settings.xml, `nuget.config`, sbt resolvers) to the build tool unchanged and take credentials from environment variables named in configuration. Credentials MUST NOT appear in logs, reports or captured tool output.

#### Scenario: Maven with a corporate Nexus
- **WHEN** `DEEPWARD_MAVEN_SETTINGS` points to a settings.xml with a Nexus mirror
- **THEN** mvn runs with that settings.xml and the graph is resolved through Nexus

#### Scenario: Secret masking in output
- **WHEN** build tool output contains the value of an environment variable declared as a secret
- **THEN** that value is replaced with `***` in captured output and logs

### Requirement: Importing an existing SBOM
The system SHALL accept a pre-built CycloneDX SBOM (JSON, spec 1.4 or later) as an alternative graph source for a project, given by configuration or a CLI flag. The build tool is not invoked for that project.

#### Scenario: SBOM built earlier in CI
- **WHEN** the run uses `--sbom services/api=build/bom.json`
- **THEN** the `services/api` graph is read from `build/bom.json` and mvn is not invoked

### Requirement: Node.js project detection
The system SHALL detect Node.js projects by `package.json`. A directory with a lockfile (`package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`), a `pnpm-workspace.yaml` or a `workspaces` field is a project root. Every other `package.json` belongs to the nearest root above it, or forms its own project if there is none.

#### Scenario: npm workspaces
- **WHEN** the root has `package.json` with `"workspaces": ["packages/*"]`, `package-lock.json`, and `packages/lib/package.json`
- **THEN** one `npm:.` project is reported whose manifests include both `package.json` files and the lockfile

#### Scenario: Independent package in a subdirectory
- **WHEN** `tools/cli/package.json` has its own `yarn.lock` inside a repository whose root uses pnpm
- **THEN** two projects are reported, `npm:.` (pnpm) and `npm:tools/cli` (yarn)

### Requirement: Package manager selection
The system SHALL determine a Node.js project's package manager from its lockfile. Without a lockfile it SHALL use the `packageManager` field of `package.json`, then `pnpm-workspace.yaml` (pnpm), and default to npm.

#### Scenario: Corepack field without a lockfile
- **WHEN** `package.json` has `"packageManager": "pnpm@9.15.0"` and no lockfile exists
- **THEN** the project uses pnpm

### Requirement: Node.js graph from the lockfile
The system SHALL build the Node.js graph from the lockfile without running the package manager. It SHALL support `package-lock.json` and `npm-shrinkwrap.json` versions 1–3, `yarn.lock` v1 and Berry, and `pnpm-lock.yaml` versions 5.x, 6 and 9. Workspace packages and local links are part of the project, not graph nodes.

#### Scenario: Nested npm installation
- **WHEN** `package-lock.json` has `node_modules/a` (requires `b@^1`), `node_modules/b` at 2.0.0 and `node_modules/a/node_modules/b` at 1.4.0
- **THEN** the graph has the edge `a → b@1.4.0`, and `b@2.0.0` is a separate node

#### Scenario: Dependency of a workspace member
- **WHEN** workspace member `packages/lib` depends on `ms@^2.1.1`
- **THEN** `ms` is a direct dependency of the project

#### Scenario: Unsupported lockfile version
- **WHEN** `pnpm-lock.yaml` declares `lockfileVersion: '4.0'`
- **THEN** the project fails with a message that names the file and the unsupported version

### Requirement: Generated lockfile when none is committed
When a Node.js or Cargo project has no lockfile, the system SHALL run the package manager to generate one without installing packages or running scripts, read it, and delete it. The working copy SHALL be left unchanged.

#### Scenario: Cargo library without Cargo.lock
- **WHEN** a crate has `Cargo.toml` but no `Cargo.lock`
- **THEN** Cargo generates the lockfile, the graph is built from it, and `Cargo.lock` is removed afterwards

### Requirement: Development and build scopes
Node.js `devDependencies`, Cargo `dev-dependencies` and Cargo `build-dependencies` SHALL be reflected in node scopes `development` and `build`. A transitive package gets the widest scope through which it is reachable (normal over build over development), and scopes can be filtered out by configuration.

#### Scenario: Package used in production and in development
- **WHEN** `ms` is reached through a production dependency and through a dev dependency
- **THEN** the `ms` node has scope `runtime`

#### Scenario: Dev-only package
- **WHEN** `minimist` is only a dev dependency
- **THEN** its node has scope `development`

### Requirement: Go module graph
The system SHALL detect Go modules by `go.mod`, skipping `vendor` and `testdata` directories, and build the graph with the Go toolchain. Nodes SHALL use the versions the module graph selects. Modules replaced by a local directory SHALL NOT be graph nodes, and modules replaced by another module SHALL use the replacement's path and version.

#### Scenario: Minimal version selection
- **WHEN** `go mod graph` lists `golang.org/x/text` at v0.3.0 and v0.3.7 and v0.3.7 is selected
- **THEN** the graph contains a single `golang.org/x/text` node at v0.3.7

#### Scenario: Indirect requirement
- **WHEN** `go.mod` lists a module with `// indirect`
- **THEN** its node is not marked as direct

### Requirement: Cargo graph from Cargo.lock
The system SHALL detect Cargo projects by `Cargo.toml`. Workspace members belong to the workspace root's project. The graph SHALL be built from `Cargo.lock`. Workspace members, path dependencies and git dependencies are part of the project, not graph nodes; the registry crates they depend on are.

#### Scenario: Workspace with two members
- **WHEN** the workspace has members `app` (depends on `regex`) and `util` (depends on `itoa`)
- **THEN** one `cargo:.` project is reported, and `regex` and `itoa` are direct dependencies

#### Scenario: Two versions of one crate
- **WHEN** `Cargo.lock` contains `syn` 1.0.109 and 2.0.48, and a dependency entry says `"syn 2.0.48"`
- **THEN** the edge points to the `syn@2.0.48` node

### Requirement: Package URLs for the new ecosystems
Graph nodes SHALL use the purl types `npm` (scope as namespace, for example `pkg:npm/%40app/lib@1.0.0`), `golang` (module path, for example `pkg:golang/golang.org/x/text@v0.3.7`) and `cargo` (`pkg:cargo/regex@1.5.4`).

#### Scenario: Scoped npm package
- **WHEN** the lockfile contains `@sindresorhus/is` at 4.0.1
- **THEN** the node ID is `pkg:npm/%40sindresorhus/is@4.0.1` and its name is `@sindresorhus/is`

### Requirement: Go module scopes from compiled packages
The system SHALL give each Go module node the scope `compile` if a package of it is built by the module's packages, `test` if only their tests need it, and `not-built` otherwise. Builds SHALL be evaluated for the configured target platforms (default linux/amd64, darwin/arm64, windows/amd64), and a module built for any of them is built.

#### Scenario: Driver declared but not imported
- **WHEN** the service imports only the pgx and iofs drivers of `golang-migrate/migrate/v4`, and the module graph also contains `github.com/docker/docker` through migrate
- **THEN** the `golang-migrate/migrate/v4` node has scope `compile` and the `github.com/docker/docker` node has scope `not-built`

#### Scenario: Test-only module
- **WHEN** `github.com/alicebob/miniredis/v2` is imported only by `_test.go` files
- **THEN** its node has scope `test`

#### Scenario: Windows-only dependency
- **WHEN** a module is imported only by files with the `windows` build constraint
- **THEN** its node has scope `compile`

### Requirement: Conservative fallback for Go scopes
If the compiled package set cannot be determined, every Go module node SHALL keep scope `compile`, and the scan SHALL continue without hiding any module.

#### Scenario: Package listing fails
- **WHEN** `go list -deps` exits non-zero for every platform
- **THEN** all module nodes have scope `compile` and the project is scanned
