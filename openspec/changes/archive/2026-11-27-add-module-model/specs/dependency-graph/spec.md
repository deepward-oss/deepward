# Spec Delta

## MODIFIED Requirements

### Requirement: Normalized graph model
Every package node SHALL be identified by a purl (`pkg:maven/...`, `pkg:nuget/...`) and SHALL carry its ecosystem, resolved version, scope, a direct-dependency flag, and the paths from the project root to the node. In a graph with module nodes, paths SHALL start at a module, and a package SHALL be direct when a module declares it.

#### Scenario: Scala artifact with cross-version suffix
- **WHEN** a Scala 2.13 sbt project depends on `"org.typelevel" %% "cats-core" % "2.10.0"`
- **THEN** the node purl is `pkg:maven/org.typelevel/cats-core_2.13@2.10.0`, and the base name `cats-core` and suffix `_2.13` are kept as separate attributes

#### Scenario: Path to a transitive dependency
- **WHEN** `app` depends on `A`, `A` on `B`, and `B` on `log4j-core`
- **THEN** the `log4j-core` node has the path `app → A → B → log4j-core` and `A` as its direct ancestor

#### Scenario: Test-scoped dependency
- **WHEN** a dependency is declared in Maven scope `test` or sbt configuration `Test`
- **THEN** the node has scope `test` and can be filtered out by configuration

#### Scenario: Path from a module
- **WHEN** module `wr-history-api` of a Maven reactor declares `kafka-clients`, which depends on `lz4-java`
- **THEN** the `lz4-java` node has the path `wr-history-api → kafka-clients → lz4-java`, and `kafka-clients` is direct

## ADDED Requirements

### Requirement: Module nodes
A graph MAY contain module nodes, one per member of a multi-module build. A module node SHALL have a stable ID unique in the project, a display name and the path of its manifest when it has one of its own. The project root SHALL point at the modules, each module at the packages it declares and at the modules it depends on. Module nodes SHALL NOT be treated as packages, and a graph without module nodes SHALL keep its current shape.

#### Scenario: Two modules sharing a dependency
- **WHEN** modules `api` and `domain` both declare `wr-commons` 2.2.0
- **THEN** the graph has one `wr-commons@2.2.0` package node with edges from both module nodes, and the root points at `api` and `domain`

#### Scenario: Dependency between modules
- **WHEN** module `app` depends on module `domain`
- **THEN** the graph has an edge from `app` to `domain`, and `domain` is not reported as a package

#### Scenario: Ecosystem without modules
- **WHEN** a Go module is scanned
- **THEN** its graph has no module nodes and the root points at its direct dependencies as before

### Requirement: Modules reaching a package
For each package node the system SHALL determine the modules that reach it, directly or through other packages or modules, and its scope in each of them. The node scope SHALL be the widest of its per-module scopes.

#### Scenario: Test library in every module
- **WHEN** `mockito-core` is a `test` dependency in all seven modules that use it
- **THEN** its scope is `test` in each of them and the node scope is `test`

#### Scenario: Different scopes per module
- **WHEN** `jackson-databind` is `compile` in `api` and `test` in `integration-tests`
- **THEN** the node records `compile` for `api`, `test` for `integration-tests`, and scope `compile`

#### Scenario: Different versions per module
- **WHEN** module `api` resolves `jffi` 1.3.9 and module `domain` resolves `jffi` 1.2.16
- **THEN** `jffi@1.3.9` is reached only by `api` and `jffi@1.2.16` only by `domain`

#### Scenario: Package reached through another module
- **WHEN** `app` depends on module `domain`, which declares `wr-commons`
- **THEN** both `domain` and `app` reach `wr-commons`

### Requirement: Maven modules
The Maven graph SHALL contain one module node per reactor module, identified by its POM path and named by its artifactId, with the module's dependencies, its per-module scopes from that module's resolved tree, and edges to the reactor modules it depends on.

#### Scenario: Nine-module reactor
- **WHEN** a reactor root lists nine modules and `wr-history-api` declares `kafka-clients`
- **THEN** the graph has nine module nodes, and the `kafka-clients` node is direct with an edge from `wr-history-api`

### Requirement: sbt subprojects as modules
The sbt graph SHALL contain one module node per subproject, identified and named by its sbt project ID, with the subproject's dependencies and its per-subproject scopes, and edges to the subprojects it depends on.

#### Scenario: Two subprojects
- **WHEN** subproject `core` depends on `cats-core` in `Compile` and subproject `web` depends on `core` and on `http4s` in `Compile`
- **THEN** the graph has module nodes `core` and `web`, an edge from `web` to `core`, and `cats-core` is reached by both with scope `compile`
