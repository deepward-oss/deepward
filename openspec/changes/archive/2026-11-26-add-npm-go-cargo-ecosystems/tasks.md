## 1. Dependency SDK

- [x] 1.1 Add ecosystems `npm`, `Go`, `crates.io`, purl types `npm`, `golang`, `cargo`, SemVer prerelease rules and canonical Go versions
- [x] 1.2 Add scopes `development` and `build`
- [x] 1.3 Make `Graph.Paths` and `Graph.DirectAncestors` scale to large graphs (best-first path search, reverse reachability)
- [x] 1.4 Add the lockfile regeneration helper (snapshot, run, diff, restore)
- [x] 1.5 Map the new ecosystems in OSV, offline OSV and GHSA sources

## 2. Toolchain

- [x] 2.1 Built-in definitions for `npm`, `yarn`, `pnpm`, `go`, `cargo`

## 3. npm ecosystem

- [x] 3.1 Detection, project roots, workspaces and package manager selection
- [x] 3.2 `package-lock.json` v1–v3 parser
- [x] 3.3 `yarn.lock` v1 and Berry parsers
- [x] 3.4 `pnpm-lock.yaml` v5.x, v6, v9 parser
- [x] 3.5 Scope propagation and graph building; lockfile generation when missing
- [x] 3.6 npm registry client with scoped registries, auth and deprecation filter
- [x] 3.7 `package.json` range rewriting and override insertion
- [x] 3.8 Realizer: direct updates, transitive pins, lockfile regeneration per package manager

## 4. Go ecosystem

- [x] 4.1 Detection of `go.mod` modules
- [x] 4.2 Graph from `go list -m -json all` and `go mod graph`, with replacements
- [x] 4.3 Module proxy client with `GOPROXY`, `GOPRIVATE`, `GONOPROXY`
- [x] 4.4 Realizer with `go get`, manual changes for replaced modules and vendoring

## 5. Cargo ecosystem

- [x] 5.1 Detection of crates and workspaces
- [x] 5.2 `Cargo.lock` parser, dependency kinds from `Cargo.toml`, lockfile generation when missing
- [x] 5.3 Sparse index client with yanked filter
- [x] 5.4 Requirement matching and `Cargo.toml` requirement rewriting
- [x] 5.5 Realizer with `cargo update --precise`

## 6. Integration and docs

- [x] 6.1 Register the plugins, regenerate the JSON Schema
- [x] 6.2 Sample projects in `testdata/projects` and recorded registry fixtures
- [x] 6.3 Docs: `docs/ecosystems/{npm,go,cargo}.md`, configuration, plugins, README
- [x] 6.4 Integration tests with real Node.js, Go and Rust toolchains
- [x] 6.5 Coverage, lint and license gates pass
