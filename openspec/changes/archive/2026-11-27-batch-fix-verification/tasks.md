# Tasks

## 1. Affected packages

- [x] 1.1 Add `Remediation.Affects []string` to `pkg/sdk/finding.go` and describe it for plugin authors in `docs/plugins.md` (keys are package URLs without version; empty means the change is verified alone); verify with `go test ./pkg/sdk/... ./test/docs`
- [x] 1.2 Fill `Affects` in `plugins/analyzer/depvuln` (direct upgrade, ancestor upgrade, blind candidates, pin) and `plugins/analyzer/outdated` with the changed package and its current descendants in the graph; verify with analyzer tests that an upgrade of `parent` lists `parent` and `child`, and that a pin of a leaf lists only the leaf

## 2. Batch verification in the engine

- [x] 2.1 Add `fix.verify` (`batch` default, `each`) to `internal/engine/config.go` with validation and the JSON schema; verify with config tests for the default, both values and an invalid value
- [x] 2.2 Count the rescans `verify` actually runs (memo misses) and report `graphBuilds` in the `fixes verified` progress line; verify with an engine test that reads the count for a run with a known number of rebuilds
- [x] 2.3 Implement rounds and greedy batching per project in `internal/engine/fix.go`: units per target (merged upgrade first), group states with earlier accepted changes, batches of groups with disjoint `Affects`, a batch of its own for units without `Affects`, sequential realization into one copy, one `verify` per batch and per-unit verdicts against the unit's own fingerprints; verify with engine tests using a fake ecosystem that 12 independent groups take 1 rebuild and 6 second alternatives take 2 rounds
- [x] 2.4 Implement bisection of batches that do not build (halves by group order, down to single units; rejected units get `the project no longer builds`) and the one-by-one fallback when every half builds; verify with engine tests that one breaking unit among 8 is found within 1 + 2·⌈log₂ 8⌉ rebuilds while the other seven are accepted, and that a pair breaking only together matches `each`
- [x] 2.5 Keep the unverified path per target (first `remaining` alternative becomes the candidate, published with `fix.unverified.publish`) and manual changes from failed realization; verify with the existing unverified tests passing in both modes
- [x] 2.6 Run batches on the worker pool (rounds of a project sequential, batches and projects parallel, one copy per project and worker) and keep `each` as the existing per-group loop; verify that the existing `fix.concurrency` 1 vs 8 test passes in both modes and that the plan order does not change

## 3. Post tasks at assembly

- [x] 3.1 Move post tasks out of `try`/`attempt` into change set assembly: for every group with items, reset a copy, apply its items, run the applicable tasks once, take the diff; a failing task drops the change set with a `FailedFix` and no further alternative; set `FixItem.Tasks` for the items; verify with post task tests updated to the new semantics (regenerated file joins the request, a failing task drops only its group, one run for three changes)
- [x] 3.2 Update `docs/configuration.md` (post tasks and `fix.verify`), `docs/publishing.md` (edit, verify and assemble steps) and `CHANGELOG.md` under `[Unreleased]` (batch verification, `graphBuilds`, `Remediation.Affects`, **BREAKING** post tasks); verify with `go test ./test/docs`

## 4. Tests that prove the gain

- [x] 4.1 Add engine tests that run the same scenarios with `batch` and `each` and assert identical change sets, diffs, manual fixes and failures, plus the exact rebuild counts (1 vs 12, 2 vs 18, bisection bound, upgrade and pin of the same package in separate batches); verify they pass with `go test ./internal/engine`
- [x] 4.2 Extend `test/e2e/fakemvn` to take versions of any dependency from the POMs, append one line per call to a counter file and sleep for `FAKEMVN_DELAY`; add an end-to-end test on a fixture with about ten vulnerable packages that runs `deepward fix` in both modes and asserts at least three times fewer calls and less than half the time with `batch`; verify with `go test ./test/e2e`
- [x] 4.3 Add a `maven-many` fixture (about six modules, about ten really vulnerable direct and transitive dependencies) and an integration test that runs a warm-up scan, `fix` with `each` and `fix` with `batch` against real Maven, asserts identical change sets and at least three times fewer `mvn` runs, and prints `mode | mvn runs | duration`; verify with `go test -tags integration ./test/integration/...` in Docker

## 5. Integration check

- [x] 5.1 Run `golangci-lint run ./...`, `go test ./...` and the coverage check, and verify all pass

## Workflow follow-up

- Archive the change with `/opsx:archive` after review.
