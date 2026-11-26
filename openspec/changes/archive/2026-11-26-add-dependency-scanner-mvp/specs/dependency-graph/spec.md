## Purpose

Detect sbt, Maven and NuGet projects in a repository working copy and build a complete, normalized dependency graph (direct and transitive) using each ecosystem's native build tool.

## ADDED Requirements

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
