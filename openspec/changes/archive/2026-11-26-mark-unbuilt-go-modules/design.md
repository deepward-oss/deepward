# Design

## Context

The `go` ecosystem builds its graph from `go list -m -json all` and `go mod graph` (change `add-npm-go-cargo-ecosystems`). Module-level graphs over-report, because a module's `go.mod` requires the dependencies of all its packages, not only the ones the project imports.

## Decisions

### D1. Compiled set from `go list -deps`
For every configured platform `GOOS/GOARCH`, the plugin runs:

```text
go list -e -deps -f '{{with .Module}}{{.Path}}{{end}}' ./...        # build
go list -e -deps -test -f '{{with .Module}}{{.Path}}{{end}}' ./...  # build + tests
```

- `built` is the union of the first command's modules over all platforms.
- `test` is the union of the second command's modules, minus `built`.
- `-e` keeps going past broken packages.
- A platform whose command fails is skipped. If every build command fails, no scopes are assigned (all `compile`).

The module path printed is the required path, so replaced modules match the graph's `selected` map by their original path.

Platforms default to linux/amd64, darwin/arm64 and windows/amd64, which covers OS-specific imports in practice. `plugins.go.platforms` overrides them, for example for `js/wasm` or `linux/arm`, and `plugins.go.tags` passes build tags (`-tags`) the project builds with.
*Alternative: govulncheck-style call-graph reachability.* Rejected for now: it needs type checking of the whole program, and module-level advisories would still need this coarser filter.

### D2. Report, mark and keep out of the gate
Not-built findings keep their real severity, because severity describes the vulnerability and must match the advisory databases. Relevance is expressed by a separate, generic `Finding.NotBuilt` flag, so the engine stays check-agnostic: a static analyzer could set it for code excluded from the build too. The flag drives:
- **gate**: `evaluateGate` ignores flagged findings, and `Summary.NotBuilt` / `NotBuiltBySeverity` count them;
- **fixes**: they are realized and verified like any finding. A change set gets `Urgent` only from built findings (critical/high security fixes bypass the limit), and change sets whose findings are all flagged are sorted last and titled "(not built)";
- **reports**: HTML renders them in their own section with an explanation, a badge, a "Build" filter and a "not built" card, while the severity cards count built findings only. SARIF adds the flag to the Deepward property bag and the tag `not-built`. JSON carries the field.

`notBuilt: warn` in the analyzer restores a quieter behavior: one warning per project instead of findings.
*Alternative: hide by default, report on opt-in.* Rejected after review: the modules are still in `go.mod`/`go.sum`, other tools flag them, and teams want them updated by the same pipeline.

### D3. Scope name
`not-built` is a scope like `test` or `development`. Existing `excludeScopes` settings and report filters keep working, and other ecosystems can adopt it later.

## Risks / Trade-offs

- Build tags that are not covered (custom `-tags`, other platforms) can mark a module not built although it is built, which keeps its findings out of the gate. Mitigation: configurable platforms and tags. The findings are still reported, so nothing disappears.
- Six extra `go list` runs per scan. They only load packages and reuse the module cache.
