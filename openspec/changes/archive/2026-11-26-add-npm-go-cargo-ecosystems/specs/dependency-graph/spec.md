## ADDED Requirements

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
