## 1. Graph

- [x] 1.1 Add `deps.ScopeNotBuilt`
- [x] 1.2 Go plugin: run `go list -e -deps` (with and without `-test`) per configured platform and collect built and test-only module paths
- [x] 1.3 Assign `compile`, `test` or `not-built` in the graph; keep `compile` when listing fails

## 2. Analysis and engine

- [x] 2.1 SDK: `Finding.NotBuilt`, `Summary.NotBuilt` and `NotBuiltBySeverity`
- [x] 2.2 depvuln: report not-built nodes as flagged findings; `notBuilt: warn` lists them in a `not-built-skipped` warning
- [x] 2.3 Engine: exclude flagged findings from the gate; change set `Urgent`/`NotBuilt`, ordering, "(not built)" title and description

## 3. Reports

- [x] 3.1 HTML: separate section, badge, filter, card; severity cards count built findings
- [x] 3.2 SARIF flag and tag; CLI summary

## 4. Docs and verification

- [x] 4.1 Document `plugins.go.platforms`, `plugins.go.tags`, scopes, `notBuilt` and the gate rule
- [x] 4.2 Unit tests, coverage, lint; rescan a real Go service to confirm
