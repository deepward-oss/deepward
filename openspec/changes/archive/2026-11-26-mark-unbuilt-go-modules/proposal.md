# Proposal

## Why

The Go graph is module-level: `go list -m all` lists every module of the module graph, including modules whose packages the project never compiles. A real scan of a production service showed 27 advisories in 17 modules, and 16 of those modules were only present because a dependency (`golang-migrate`) declares drivers the service does not import.

Such modules still sit in `go.mod`/`go.sum`, so other scanners flag them and teams want them updated. But treating them like code that ships buries the one real finding and fails CI for code that is never built.

## What Changes

- The Go ecosystem determines which modules are compiled. It lists the packages built by `./...` and by its tests, for a set of target platforms (default linux/amd64, darwin/arm64, windows/amd64) and optional build tags.
- Module scopes:
  - `compile` for modules in the build;
  - `test` for modules only tests need;
  - new scope `not-built` for modules that are only in the module graph.
- When package listing fails, all modules keep scope `compile`, so nothing is hidden.
- The SDK finding model gets a generic `notBuilt` flag.
- The dependency-vulnerabilities analyzer reports advisories on `not-built` packages as findings flagged `notBuilt`, with their real severity and remediations. `notBuilt: warn` lists them in a warning instead.
- The engine:
  - excludes not-built findings from the quality gate and counts them separately in the summary;
  - fixes them like others, but their change requests never bypass the open request limit, are sorted last and are titled "(not built)".
- Reports highlight them:
  - HTML: a separate section with an explanation, a badge, a filter and its own summary card;
  - CLI: a separate count;
  - JSON and SARIF: the flag, plus a SARIF tag.

## Capabilities

### New Capabilities
<!-- None. -->

### Modified Capabilities
- `dependency-graph`: Go module scopes from the compiled package set.
- `vulnerability-analysis`: not-built packages are reported as flagged findings, outside the quality gate.
- `scan-report`: not-built findings are highlighted in every format.
- `change-requests`: fixes for not-built packages without priority.

## Impact

- `plugins/ecosystem/golang` (graph production, config `platforms`, `tags`).
- `pkg/sdk` (`Finding.NotBuilt`, `Summary.NotBuilt`, `deps.ScopeNotBuilt`).
- `internal/engine` (gate, change set ordering, limit), `internal/cli` (summary).
- `plugins/analyzer/depvuln` (config `notBuilt`).
- Reporters: html, sarif.
- Docs: `docs/ecosystems/go.md`, `docs/configuration.md`, `docs/publishing.md`.
- Scans run `go list -deps` once per platform and mode (six runs by default). The calls only load packages; nothing is compiled.
