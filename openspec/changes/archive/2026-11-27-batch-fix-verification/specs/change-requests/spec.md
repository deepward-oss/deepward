# Spec Delta

## ADDED Requirements

### Requirement: Verification in batches
With `fix.verify: batch` (the default), the system SHALL verify in rounds: each round takes the next untried alternative of every target without a verified change, applies each batch of independent changes together in one copy and rebuilds the graph once per batch. A change SHALL be accepted when the project builds and its targeted findings are gone. With `fix.verify: each`, every alternative SHALL get its own rebuild. For independent changes, both modes SHALL produce the same plan.

#### Scenario: Twelve independent fixes
- **WHEN** a project has twelve change groups whose first alternatives are independent and pass
- **THEN** verification rebuilds the graph once with `batch` and twelve times with `each`, and both runs produce the same change sets

#### Scenario: Second alternatives
- **WHEN** six of twelve independent groups need their second alternative
- **THEN** `batch` rebuilds the graph twice, once per round, and `each` eighteen times

### Requirement: Bisection of failing batches
When a batch does not build, the system SHALL split it in halves and verify each half until the changes that break the build are found; those SHALL be rejected as `the project no longer builds`, and the other changes SHALL keep the verdicts of the smallest batch that built. When every half builds but the whole batch does not, the changes of that batch SHALL be verified one by one.

#### Scenario: One change breaks the build
- **WHEN** one of eight independent changes in a batch makes the build fail
- **THEN** the halves containing it are verified until it is found, it is rejected as `the project no longer builds`, and the seven others are accepted

#### Scenario: Changes that break only together
- **WHEN** a batch does not build but each of its halves does
- **THEN** its changes are verified one by one and the result matches `fix.verify: each`

### Requirement: Independent changes
Changes of different change groups SHALL share a batch only when the packages they declare as affected do not overlap; a pin or an upgrade SHALL declare its package and every package it currently brings in. Changes of the same change group SHALL be verified together, as they are published together. A change that declares no affected packages SHALL be verified in a batch of its own.

#### Scenario: Upgrade and pin of the same package
- **WHEN** one group upgrades `parent` from 1.0 to 2.0, which brings `child`, and another group pins `child`
- **THEN** the two changes are verified in different batches, so the upgrade is accepted only if it clears its findings without the pin

#### Scenario: Text edit without affected packages
- **WHEN** a remediation is a text edit that declares no affected packages
- **THEN** it is verified in a batch of its own

### Requirement: Graph rebuilds in the verification summary
The system SHALL report in the `fixes verified` progress line the number of graph rebuilds verification needed (`graphBuilds`), not counting the scan before verification or results reused from a rebuild of identical files.

#### Scenario: Comparing modes
- **WHEN** the same workspace is fixed with `fix.verify: each` and with the default
- **THEN** both `fixes verified` lines report `graphBuilds`, and the default reports fewer for independent changes

## MODIFIED Requirements

### Requirement: Post tasks
The system SHALL run the `fix.postTasks` that apply to a project (`ecosystems` and `projects` filters) once per change request, after its changes are verified and before they are committed and the request is assembled, in a copy of the project that holds only that request's changes, resolving each `tool` through the toolchain and running it in `dir` relative to the project directory with `args` and `env`. Post tasks SHALL not run before verification. Changes the task makes to files matching `files` (globs relative to the repository root, `**` for any number of directories; every file outside excluded directories when empty), including new and deleted files, SHALL become part of the commit of the change request; other changes SHALL be undone. A task that fails SHALL drop the change request, which is reported as failed with the command and the last line of its output as the reason, and no other alternative is tried for it. The change request SHALL list the tasks that ran.

#### Scenario: Regenerated file joins the change
- **WHEN** a post task regenerates `app/generated.txt` from the edited versions, writes a log file and `files` is `["app/*.txt"]`
- **THEN** the change request contains the edited manifest and `app/generated.txt` as generated for the verified version, not the log file

#### Scenario: Failing post task
- **WHEN** a post task exits with an error
- **THEN** no change request is created for that group, it is reported as failed with `post task <command> failed` and the last output line, and other groups are not affected

#### Scenario: Once per change request
- **WHEN** a change request carries three verified changes and one post task applies
- **THEN** the task runs once, after verification and before the commit, with the three changes applied and no change of another request, and the files it regenerated are in the same commit as the three changes

### Requirement: Parallel verification
The system SHALL verify with `fix.concurrency` workers (default 2), which take batches (with `fix.verify: batch`) or change groups (with `fix.verify: each`). Each worker SHALL copy a project once, reuse the copy for everything of that project it verifies and restore the edited files in between. The resulting plan SHALL not depend on the number of workers.

#### Scenario: Same plan with any concurrency
- **WHEN** the same workspace is fixed with `fix.concurrency` 1 and 8
- **THEN** both runs produce the same change sets, diffs, manual fixes and failures, in the same order, and no change set carries another group's edit
