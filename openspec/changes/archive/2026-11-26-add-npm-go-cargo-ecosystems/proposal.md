# Proposal

## Why

The MVP covers sbt, Maven and NuGet. Many teams also ship JavaScript/TypeScript, Go and Rust code from the same repositories. Without these ecosystems Deepward cannot replace Renovate for them, and vulnerable npm, Go and Rust dependencies stay out of the unified report and quality gate.

## What Changes

- New `npm` ecosystem plugin for Node.js projects. It supports the npm, Yarn (classic and Berry) and pnpm package managers, including workspaces.
  - It builds the graph from the lockfile the package manager wrote: `package-lock.json`/`npm-shrinkwrap.json` (v1–v3), `yarn.lock` (v1 and Berry), `pnpm-lock.yaml` (v5.x, v6, v9).
  - If a project has no lockfile, the package manager generates one temporarily.
  - It lists versions from the npm registry protocol, with scoped private registries and tokens.
  - Fixes bump the range in `package.json` and pin transitive packages through `overrides`, `resolutions` or `pnpm.overrides`. Each fix regenerates the lockfile with the project's package manager.
- New `go` ecosystem plugin for Go modules.
  - It builds the graph with the Go toolchain (`go list -m` and `go mod graph`).
  - It lists versions from the module proxy protocol and respects `GOPROXY`, `GOPRIVATE` and `GONOPROXY`.
  - Fixes run `go get module@version`, which updates `go.mod` and `go.sum`.
- New `cargo` ecosystem plugin for Rust.
  - It builds the graph from `Cargo.lock` and supports workspaces. When `Cargo.lock` is missing, Cargo generates it temporarily.
  - It lists versions from the crates.io sparse index (or a configured sparse registry) and skips yanked versions.
  - Fixes are lockfile-only (`cargo update --precise`) when the requirement allows the new version. Otherwise the requirement in `Cargo.toml` is bumped first.
- Dependency SDK: new ecosystems `npm`, `Go`, `crates.io` with their purl types and version rules, new scopes `development` and `build`, and a dependency-graph path search that scales to large JavaScript graphs.
- Vulnerability sources (OSV API, offline OSV, GHSA) handle the new ecosystems.
- Toolchain: built-in definitions for `npm`, `yarn`, `pnpm`, `go` and `cargo`.

## Capabilities

### New Capabilities
<!-- None. -->

### Modified Capabilities
- `dependency-graph`: detection and graph building for npm/Yarn/pnpm, Go modules and Cargo; lockfiles as the graph source.
- `update-discovery`: npm registry, Go module proxy and crates.io sparse index as version sources.
- `vulnerability-analysis`: matching advisories of the npm, Go and crates.io ecosystems.
- `change-requests`: manifest and lockfile changes for the new ecosystems.
- `toolchain`: built-in tool definitions for the new package managers.

## Impact

- New plugins under `plugins/ecosystem/{npm,golang,cargo}`, registered in `plugins/all`.
- `pkg/sdk/deps` and `pkg/sdk/purl` gain the new ecosystems. Existing behavior for Maven, sbt and NuGet is unchanged.
- New permissive dependencies: `github.com/BurntSushi/toml` (MIT) and `golang.org/x/mod` (BSD-3-Clause).
- Docs: `docs/ecosystems/{npm,go,cargo}.md`, configuration reference, JSON Schema.
- Integration tests run real Node.js, Go and Rust toolchains in containers.
