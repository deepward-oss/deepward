# Proposal

## Why

`deepward fix` rebuilds the dependency graph once for every alternative it tries. On a Maven reactor one `mvn dependency:tree` takes about a minute, so twelve change groups with one and a half attempts each cost eighteen rebuilds and ten minutes or more, even with two workers. Most of those changes touch unrelated packages and could be verified together in one rebuild.

## What Changes

- `fix.verify: batch` (new default): changes are verified in rounds. Each round takes the next untried alternative of every unresolved target, splits them into batches of independent changes and rebuilds the graph once per batch. A batch that breaks the build is split in halves until the change that breaks it is found. Changes that only break together are verified one by one.
- Two changes of different groups are independent when the packages they can affect do not overlap. The analyzer declares those packages in a new `Remediation.Affects` (a pin or an upgrade affects the package and its current dependencies). A remediation without `Affects` is verified on its own.
- `fix.verify: each` keeps today's behavior: every alternative is verified by its own rebuild. Both modes produce the same plan when the changes are independent.
- **BREAKING (post tasks):** `fix.postTasks` run once per change request, like Renovate's `postUpgradeTasks`: after verification, on a clean copy with only that request's changes, before the commit is made and the request is assembled, so their output is part of the commit. They no longer run before each verification, and a failing task drops the change request instead of making Deepward try the next alternative.
- The `fixes verified` progress line reports `graphBuilds`, the number of graph rebuilds verification needed, so the two modes can be compared on a real project.
- `pkg/sdk`: `Remediation.Affects` (additive).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `change-requests`: verification in batches (`fix.verify`), post tasks once per change request before its commit and assembly, `graphBuilds` in the verification summary.

## Impact

- `internal/engine/fix.go` (rounds, batches, bisection, assembling change sets on a clean copy), `internal/engine/posttask.go` (tasks at assembly), `internal/engine/config.go` (`fix.verify`), `internal/engine/scratch.go` (applying the edits of several groups).
- `pkg/sdk` (`Remediation.Affects`), `plugins/analyzer/depvuln` and `plugins/analyzer/outdated` (fill `Affects` from the graph).
- Tests: engine tests counting graph builds and comparing both modes, an end-to-end test with a call counter and delay in `test/e2e/fakemvn`, and a real Maven benchmark fixture `maven-many` under `test/integration`.
- Users: fewer rebuilds by default; post tasks run once per change request, so a task that must run before verification is no longer supported.
- Docs: `docs/configuration.md`, `docs/publishing.md`, `docs/plugins.md`, `CHANGELOG.md`.
- No new dependencies.
