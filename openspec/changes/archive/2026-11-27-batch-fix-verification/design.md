# Design

## Context

See proposal.md for the motivation. `PlanFixes` scans, then `groups` builds fix groups (project + group name) of targets; a target is a set of findings sharing an ordered list of alternatives (`fix.transitive` order), optionally with a merged `upgrade` of an outdated finding that is tried first and must also clear `upgradeFindings`. `fixGroups` runs `fix.concurrency` workers; each keeps one `scratch` copy per project and verifies one group at a time: for every target, `attempt` tries alternatives in order, and `try` realizes the edit, applies it, runs post tasks and calls `verify`, which rescans the copy (`ScanWorkspace` filtered to the project and the targeted analyzers, memoized per copy by file hash) and compares fingerprints. Accepted edits accumulate in the copy for the rest of the group; rejected ones are rolled back; `reset` restores the copy between groups. A change set is the copy's diff after the group.

## Goals / Non-Goals

**Goals:**
- Verify independent changes of all groups of a project with as few graph rebuilds as possible, without changing what gets published.
- Keep `each` as an exact fallback and make both modes produce the same plan for independent changes.
- Make the gain measurable in tests (rebuild counts) and in production logs (`graphBuilds`).

**Non-Goals:**
- Making one rebuild cheaper (`-T`, offline mode, daemons) or caching graphs between runs.
- Speeding up `deepward scan`.
- Batching across projects: a rebuild covers one project, so batches never mix projects.

## Decisions

**Units, rounds and batches.** A *unit* is one alternative of one target of one group. Per project, verification runs in rounds. Round *k* takes, for every target still without an accepted change, its next untried alternative (the merged upgrade first, then the security alternatives, as `attempt` does today). Units are partitioned into batches; a batch holds whole groups (all units of a group in that round) whose affected sets do not overlap with those of other groups in the batch. Each group's state in a batch is its changes accepted in earlier rounds plus its units of this round. Partitioning is greedy in group order (first batch that does not conflict), which is deterministic and close to optimal for the sparse conflicts expected. Alternative: verify everything of a round in one batch and attribute results afterwards. Rejected because an upgrade could pass only thanks to a pin in another group, which would publish a request that does not work on its own.

**Affected packages.** `sdk.Remediation` gains `Affects []string`: opaque keys (package URLs without version) of what the change may alter. `depvuln` and `outdated` fill it from the graph: the changed package plus its current descendants (`Graph.Children` walked transitively). The engine only intersects sets, so it stays check-agnostic; a remediation without `Affects` (text edits, third-party analyzers) gets a batch of its own. Units of the same group never conflict with each other, because the group is published as one request and is verified cumulatively today too.

**Applying a batch.** The copy is reset, then every group's accepted changes and units are realized and applied in group order, target order, so a later edit is computed against the file as earlier edits left it (as within a group today). A unit whose realization fails becomes a manual change and leaves the batch before the rebuild. Then one `verify` call rescans the copy; its findings are matched per unit against the unit's own targeted fingerprints. Results are memoized by file hash as today, so a bisection step that recreates an earlier state is free.

**Bisection.** If the project no longer builds, the batch's groups are split in two halves (by group order) and each half is rebuilt on its own, recursively, down to single groups and then single units of a group. A single unit that fails is rejected with `the project no longer builds: <reason>`; the others continue with their own verdicts from the smallest batch that built. If both halves build but the whole did not, the batch falls back to verifying its units one by one, as `each` does. Cost: a single breaking unit among *n* adds at most 2·⌈log₂ n⌉ rebuilds.

**Verdicts and the unverified path.** Per unit: accepted when it builds and its findings are gone; `remaining` when it builds but findings stay (it becomes the target's unverified candidate if it is the first such, as today); rejected otherwise. A rejected or remaining unit moves its target to the next round. When a target runs out of alternatives, its last manual change or failure is reported, and with `fix.unverified.publish` its candidate is kept as an unverified item, exactly as `attempt` does.

**`each` mode.** The existing per-group loop stays as the implementation of `fix.verify: each`, minus the post tasks, which move to assembly in both modes. The equivalence tests run both modes on the same scenarios.

**Assembling change sets and post tasks.** After verification, for every group with accepted or unverified items: reset a copy, realize and apply the group's items in target order, run the applicable post tasks once (`runPostTasks` with `sync` keeping `files`), and take the copy's diff as the change set. The change set is what `Publish` commits and turns into a request, so the tasks run before the commit and before the request is assembled, and their output is in the same commit as the version edits (as with Renovate's `postUpgradeTasks`); `deepward fix` without `--publish` shows the same diff. A failing task drops the change set and adds a `FailedFix` with the task's message; no other alternative is tried. `FixItem.Tasks` is set to the tasks that ran for every item of the change set, so request descriptions keep listing them. Assembly reuses the worker pool; it involves no rebuild.

**Workers.** With `batch`, a job is one batch (or one bisection step) of one project; rounds of one project are sequential, projects and the batches of one round run in parallel. Each worker keeps one copy per project, as today. The plan is ordered by group as today, so it does not depend on the number of workers.

**`graphBuilds`.** The engine counts rescans that `verify` actually ran (memo misses) and reports the count in `fixes verified`. Engine tests read the count, and a fake ecosystem counts `Produce` calls to check it.

**Tests that prove the gain.** Engine tests with a fake ecosystem whose graph is computed from version files assert exact rebuild counts per scenario (12 independent groups: 1 vs 12; six second alternatives: 2 vs 18; one breaking change: within 1 + 2·⌈log₂ n⌉; upgrade plus pin of the same package: separate batches) and that both modes yield identical plans. `test/e2e/fakemvn` learns to take versions of any dependency from the POMs, to append one line per call to a counter file and to sleep for `FAKEMVN_DELAY`; the end-to-end test fixes a fixture with about ten vulnerable packages in both modes and asserts at least three times fewer calls and less than half the time with `batch`. A `maven-many` fixture (about six modules, about ten really vulnerable dependencies) runs under `-tags integration` with real Maven: warm-up scan, `each`, `batch`; it asserts identical change sets and at least three times fewer `mvn` runs and prints `mode | mvn runs | duration`.

## Risks / Trade-offs

- [A change of another group brings new packages that are not in the current graph and happen to clear a unit's findings] → Affected sets come from the current graph, so this is not detected; it needs a new version that newly depends on the target package, which is rare. `fix.verify: each` remains for exact verification.
- [A change of another group newly brings a vulnerable version of a unit's target package, so the unit's findings stay and a working fix is rejected] → Seen on real Maven: commons-compress 1.20 has no dependencies, 1.26.0 brings commons-lang3 3.14, and in the same batch the commons-text upgrade that fixes commons-lang3 looked ineffective, so the pin was used instead. The result is still verified, only different from `each`; the `maven-many` fixture avoids such packages, and the docs name `fix.verify: each` for exact per-request verification.
- [Post tasks no longer see the state before verification, so a task whose output changes the graph is verified without it] → Documented as breaking; lockfiles of npm, pnpm, Yarn and Cargo are regenerated by the ecosystems during realization, not by post tasks, so verification still sees them.
- [One slow, broken change makes bisection rebuild several times] → Bounded by 2·⌈log₂ n⌉ per breaking change; still far below one rebuild per change.
- [Realizing several groups' edits into the same file (root POM `dependencyManagement`)] → Edits are realized sequentially against the current content, as items of one group are today; two groups never edit the same package, because their affected sets would overlap.

## Migration Plan

`fix.verify` defaults to `batch`; `fix.verify: each` restores the old verification. Post tasks move to assembly in both modes; the changelog marks this as breaking and explains that a failing task now drops the request.
