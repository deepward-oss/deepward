# change-requests Specification

## Purpose
Prepare manifest edits that update dependencies and fix vulnerabilities, and, on explicit request, publish them as GitLab Merge Requests or GitHub Pull Requests.

## Requirements

### Requirement: Publishing only on explicit request
The system MUST NOT modify the remote repository (branches, commits, MRs/PRs) unless the publish command is invoked and a token is provided. By default edits are only shown as a diff (dry run).

#### Scenario: Run without the publish flag
- **WHEN** `deepward fix` runs without `--publish`
- **THEN** the system prints a unified diff of proposed edits and makes no write API calls and no `git push`

#### Scenario: Publishing without a token
- **WHEN** `deepward fix --publish` runs and neither `DEEPWARD_GITLAB_TOKEN` nor `DEEPWARD_GITHUB_TOKEN` is set
- **THEN** the command exits with the configuration error code before any change

### Requirement: GitLab and GitHub support
The system SHALL support GitLab (gitlab.com and self-managed, API v4) and GitHub (github.com and Enterprise Server, REST API v3). The platform and project are derived from the remote URL or the explicit `--platform`, `--repo` and `--api-url` parameters.

#### Scenario: Self-managed GitLab
- **WHEN** remote `origin` is `https://git.acme.local/team/billing.git` and `--platform gitlab` is set
- **THEN** the MR is created through `https://git.acme.local/api/v4` in project `team/billing`

### Requirement: Surgical manifest edits
The system SHALL change only the version token, preserving the file's formatting, comments and line endings. Supported locations: Maven literal `<version>` and `${...}` properties in the same repository; sbt string literals in `build.sbt`, `project/*.scala` and `project/plugins.sbt`; NuGet `PackageReference` in project files and `PackageVersion` in `Directory.Packages.props`.

#### Scenario: Version via Maven property
- **WHEN** `pom.xml` contains `<jackson.version>2.15.0</jackson.version>` and the updated dependency references `${jackson.version}`
- **THEN** only the property value changes and the diff is a single line

#### Scenario: Version in a Scala val
- **WHEN** `project/Dependencies.scala` contains `val circeVersion = "0.14.5"` used by three circe artifacts
- **THEN** only the `"0.14.5"` literal changes and all three artifacts are updated by one edit

#### Scenario: Version location not found
- **WHEN** the version is defined in a parent POM from an external repository or computed by an expression
- **THEN** no edit is produced and the update is marked `manual` with a reason in the report

### Requirement: Fixing transitive vulnerabilities
For a transitive finding the system SHALL try two kinds of change: a pin of the transitive version (Maven via `dependencyManagement`, NuGet via `PackageVersion` with `CentralPackageTransitivePinningEnabled` or an explicit `PackageReference`) and an upgrade of the direct ancestor to a version that brings the fix. `fix.transitive` SHALL decide which kind is tried first: `upgrade` (default) or `pin`; the other kind is tried when the first does not pass verification, keeping the analyzer's order within each kind. sbt pinning is not performed in the MVP and the finding is marked `manual`.

#### Scenario: Upgrade first by default
- **WHEN** a transitive finding has the remediations "upgrade the ancestor to 1.1", "upgrade it to 1.2" and "pin the package to 2.1", and `fix.transitive` is not set
- **THEN** the upgrades are tried first in their order, and the pin last

#### Scenario: Pin first
- **WHEN** `fix.transitive` is `pin`
- **THEN** the pin is tried first, then the ancestor upgrades

#### Scenario: Pinning in Maven
- **WHEN** the ancestor has no fixing version and `snakeyaml` is fixed in 2.0
- **THEN** `org.yaml:snakeyaml:2.0` is added to `dependencyManagement` of the root `pom.xml` with a comment naming the advisory ID

#### Scenario: Transitive vulnerability in sbt
- **WHEN** a transitive finding in an sbt project cannot be fixed by upgrading a direct dependency
- **THEN** no MR is created and the report contains a recommendation with a ready-to-paste `dependencyOverrides` line

### Requirement: Verification by graph rebuild
Before publishing, the system SHALL rebuild the project's graph on the edited files and publish only if the targeted advisories are gone and the build tool succeeded. Otherwise no MR/PR is created and the reason is reported.

#### Scenario: Vulnerability persists after the edit
- **WHEN** the rebuilt graph still contains the vulnerable version after upgrading the direct dependency
- **THEN** the change is not published and is marked `verification-failed`

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

### Requirement: Unverified changes
When verification finds the targeted findings still reported but the project still builds, the system SHALL log a warning with the reason and list the change as `verification-failed`, unless `fix.unverified.publish` is true. Then it SHALL publish the first such alternative in the order of preference (`fix.transitive`) in a change request titled `Deepward: [unverified] …`, labeled with `fix.unverified.labels` (default `deepward-unverified`), whose description starts with a warning that the fix is not confirmed, gives the reason for each unverified change and a command to inspect where the package comes from, and lists the advisories as targeted rather than fixed. Changes that break the build or whose post tasks fail SHALL never be published.

#### Scenario: Pin that does not clear the advisory
- **WHEN** `fix.unverified.publish` is true and after pinning `broken` to 2.0 the advisory is still reported while the project builds
- **THEN** a change request `Deepward: [unverified] …` with the label `deepward-unverified` pins `broken`, says the advisory is still reported after the change and suggests `mvn dependency:tree -Dverbose -Dincludes=g:broken`

#### Scenario: Default
- **WHEN** `fix.unverified.publish` is not set
- **THEN** no change request is created for the change, the summary lists it as a verification failure and says how to publish it as unverified

### Requirement: Parallel verification
The system SHALL verify with `fix.concurrency` workers (default 2), which take batches (with `fix.verify: batch`) or change groups (with `fix.verify: each`). Each worker SHALL copy a project once, reuse the copy for everything of that project it verifies and restore the edited files in between. The resulting plan SHALL not depend on the number of workers.

#### Scenario: Same plan with any concurrency
- **WHEN** the same workspace is fixed with `fix.concurrency` 1 and 8
- **THEN** both runs produce the same change sets, diffs, manual fixes and failures, in the same order, and no change set carries another group's edit

### Requirement: Choosing which findings get change requests
The system SHALL create change requests only for findings that pass `fix.kinds` (finding kinds; all when empty) and `fix.minSeverity` (severity at or above; all when empty). Updates of outdated dependencies have no severity, so any `minSeverity` leaves them out. `deepward fix --min-severity` SHALL override `fix.minSeverity`. Open bot requests whose group consists only of findings left out SHALL be neither updated nor closed.

#### Scenario: Only critical vulnerabilities
- **WHEN** `fix.minSeverity` is `critical` and a package has a critical and a high advisory, another package a medium one, and two dependencies are outdated
- **THEN** one change request fixes the package with the critical advisory, the plan reports four filtered findings, and open requests for the outdated dependencies stay open

#### Scenario: Only updates
- **WHEN** `fix.kinds` is `[outdated]`
- **THEN** only updates of outdated dependencies get change requests

### Requirement: Deterministic branches without duplicates
Branch names SHALL be derived deterministically from the ecosystem, the project and the update group (see "Configurable branch names"), for example `deepward/maven/jackson`. If an open MR/PR for that branch exists, the system SHALL update it (force-push the branch, refresh the description) instead of creating a new one.

#### Scenario: Re-run after a newer release
- **WHEN** MR `deepward/maven/jackson` targets 2.17.0 and 2.17.1 is released
- **THEN** the branch is recreated from the current target branch with 2.17.1, the MR description is updated, and no new MR is created

#### Scenario: Update no longer needed
- **WHEN** a bot MR is open but the package was already updated manually in the target branch
- **THEN** the MR is closed with a comment explaining why

#### Scenario: MR modified by a human
- **WHEN** the bot branch contains commits by another author
- **THEN** the system does not force-push and comments on the MR that automatic updates are paused

### Requirement: Grouping and limits
The system SHALL group updates into one MR/PR according to configured rules (purl pattern or shared version property/val) and SHALL respect the limit of concurrently open bot MRs/PRs (default 5). Fixes for critical and high vulnerabilities SHALL bypass the limit.

#### Scenario: Limit reached
- **WHEN** the limit is 5, 5 bot MRs are open, and there are 3 regular updates and 1 critical fix
- **THEN** only the critical fix MR is created and the 3 updates are reported as deferred

### Requirement: MR/PR content
The MR/PR description SHALL contain a change table (package, from → to, update type), fixed advisories with links and severity, links to release notes or changelogs when the registry or package repository provides them, and a hidden metadata marker for later runs. MRs/PRs SHALL get configured labels (default `dependencies`, plus `security` for fixes).

#### Scenario: Vulnerability-fix MR
- **WHEN** an MR updates `log4j-core` 2.14.1 → 2.17.2
- **THEN** the description has a table row for that update, a link to `CVE-2021-44228` with severity `critical`, and labels `dependencies` and `security`

### Requirement: Node.js direct dependency updates
To update a direct Node.js dependency, the system SHALL rewrite its range in every `package.json` of the project that declares it, keeping the range operator (`^`, `~`, exact). It SHALL then regenerate the lockfile with the project's package manager, without running lifecycle scripts. Ranges it cannot rewrite safely (`*`, `latest`, `||`, hyphen ranges, `workspace:`, `file:`, git URLs) are reported as manual changes.

#### Scenario: Caret range
- **WHEN** `package.json` declares `"axios": "^0.21.0"` and the target is 0.21.2
- **THEN** the range becomes `"^0.21.2"`, other bytes of `package.json` are unchanged, and the lockfile is regenerated

#### Scenario: Union range
- **WHEN** a dependency is declared as `"^1.0.0 || ^2.0.0"`
- **THEN** the change is reported as manual with a reason

### Requirement: Node.js transitive pins
To pin a transitive Node.js package, the system SHALL add or update an entry limited to the vulnerable version: `overrides` for npm, `pnpm.overrides` for pnpm, and `resolutions` for Yarn. With npm it SHALL first try a lockfile-only update and use `overrides` only if that leaves an older version. With Yarn, which cannot limit an override to one version, it SHALL report a manual change if the package exists in another major version.

#### Scenario: npm override
- **WHEN** `follow-redirects` 1.14.7 is vulnerable, fixed in 1.14.8, and cannot be updated within its ranges
- **THEN** `package.json` gets `"overrides": {"follow-redirects@1.14.7": "1.14.8"}` and the lockfile is regenerated

### Requirement: Go module updates
To change a Go module version, direct or transitive, the system SHALL run `go get module@version` and include the resulting `go.mod` and `go.sum` changes. Modules that are replaced in `go.mod`, and projects that vendor their dependencies, are reported as manual changes with the commands to run.

#### Scenario: Security update of a direct module
- **WHEN** `golang.org/x/text` v0.3.7 must be updated to v0.3.8
- **THEN** the change set contains the `go.mod` requirement `golang.org/x/text v0.3.8` and the matching `go.sum` lines

### Requirement: Cargo updates
To change a crate version, the system SHALL update `Cargo.lock` with `cargo update --precise`. If the requirement in `Cargo.toml` (or in `[workspace.dependencies]`) does not allow the target version, the requirement SHALL be rewritten first, keeping its operator. A transitive crate whose parents' requirements do not allow the target is reported as a manual change.

#### Scenario: Lockfile-only update
- **WHEN** `Cargo.toml` requires `regex = "1.5"`, the lock has 1.5.4, and the target is 1.5.5
- **THEN** only `Cargo.lock` changes

#### Scenario: Exact requirement
- **WHEN** `Cargo.toml` requires `smallvec = "=1.6.0"` and the target is 1.6.1
- **THEN** the requirement becomes `"=1.6.1"` and `Cargo.lock` is updated

### Requirement: Lockfile changes are verified
Lockfile and manifest changes made with a package manager SHALL go through the same verification by graph rebuild as other changes. A package manager failure makes that alternative fail, and the next alternative is tried.

#### Scenario: Package manager fails
- **WHEN** `pnpm install --lockfile-only` exits non-zero for an update
- **THEN** that update is not proposed, and the failure reason with the last lines of output is shown in the fix plan

### Requirement: Fixes for packages that are not built
Findings marked not built SHALL be fixed like other findings, for example a Go module pinned with `go get`. A change request whose findings are all not built SHALL say so in its title and description, SHALL be sorted after change requests for built packages, and SHALL NOT bypass the open request limit, whatever its severity.

#### Scenario: High advisory in a module that is not built
- **WHEN** the only finding of a change request is a high advisory on `google.golang.org/grpc`, marked not built, and the open request limit is reached
- **THEN** the change request is deferred, and its title ends with "(not built)"

### Requirement: Configurable branch names
Branch names SHALL come from the template `fix.branch` (default `deepward/{ecosystem}/{project}/{group}`), which replaces the branch prefix. `{project}` is the project directory and is empty for a project at the repository root; an empty placeholder drops the adjacent `/`. The template MUST contain `{group}`, start with fixed text and use only known placeholders.

#### Scenario: Same package in two projects
- **WHEN** npm projects `web/a` and `web/b` both depend on a vulnerable `axios` and the default template is used
- **THEN** their requests use branches `deepward/npm/web/a/security-axios` and `deepward/npm/web/b/security-axios`

#### Scenario: Root project
- **WHEN** the vulnerable project is at the repository root
- **THEN** the branch is `deepward/npm/security-axios`

### Requirement: Branch collisions stop the fix
When the branch template gives two change requests the same branch, `deepward fix` SHALL stop before publishing with an error that names both requests and suggests adding `{project}` or `{ecosystem}`.

#### Scenario: Colliding template
- **WHEN** `fix.branch` is `bot/{group}` and the same two projects are fixed
- **THEN** `deepward fix` fails before publishing and the error suggests adding `{project}`

### Requirement: Grouping strategy
`fix.grouping` SHALL select how fixes not matched by a `fix.groups` rule are split: `package` (default) opens one request per package, `type` opens per project one request for security fixes and one for other updates, `project` opens one request per project. Every package change in a batch SHALL be verified on its own, and a batch title SHALL count its changes, for example "Deepward: fix vulnerabilities in 2 packages, update 1 dependency in app".

#### Scenario: Batch per type
- **WHEN** `fix.grouping` is `type` and a project has two vulnerable packages and one outdated package
- **THEN** two requests are planned: `…/security` with two changes and `…/updates` with one

#### Scenario: Rules take precedence
- **WHEN** `fix.grouping` is `project` and a `fix.groups` rule matches two of the packages
- **THEN** those packages go to the rule's request and the rest to the `all` request

### Requirement: Update merged into the security fix
When a package has both a vulnerability fix and an update starting from the same version, the system SHALL plan one change for it in the security request: the update version is tried first and must clear the vulnerabilities and the update finding. If it fails verification, the security fix SHALL be applied alone and the update reported under its own group with the reason.

#### Scenario: Update also fixes the vulnerability
- **WHEN** `lib` 1.0 is vulnerable (fixed in 1.5) and 1.9 is available
- **THEN** one request "fix vulnerabilities: lib 1.0 → 1.9" is planned, its table shows "security fix, minor update", and no separate update request exists

#### Scenario: Update reintroduces a vulnerability
- **WHEN** the proposed update version is still vulnerable
- **THEN** the security request upgrades to the fixed version and the update is listed as `verification-failed`

### Requirement: Closing moved requests
When an open bot request is no longer planned because all its changes are now part of other planned requests, the system SHALL close it with a comment naming those requests.

#### Scenario: Grouping switched to project
- **WHEN** `deepward/maven/app/updates-other` is open and the grouping changes to `project`
- **THEN** the old request is closed with a comment saying its changes are now part of the project's request

### Requirement: Title patterns per kind of change request
The system SHALL take a change request's title from `fix.title`, which holds one pattern per kind: `security` (a vulnerability fixed by one package change), `pin` (a vulnerability fixed by pinning a transitive version; falls back to `security`), `update` (one outdated dependency) and `group` (several package changes in one request). A kind without a pattern SHALL keep Deepward's default title.

#### Scenario: Conventional Commits titles
- **WHEN** `fix.title.security` is `fix(deps): bump {name} from {from} to {to}` and a request upgrades `org.apache.kafka:kafka-clients` 4.4.0 → 4.5.0 in a root project
- **THEN** its title is `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0`

#### Scenario: Pin falls back to security
- **WHEN** only `fix.title.security` is set and a request pins `org.apache.zookeeper:zookeeper` to 3.9.5
- **THEN** its title is rendered from the `security` pattern

#### Scenario: Pin pattern
- **WHEN** `fix.title.pin` is `fix(deps): pin {name} to {to}` and a request pins `zookeeper` to 3.9.5
- **THEN** its title is `fix(deps): pin zookeeper to 3.9.5`

#### Scenario: Kind without a pattern
- **WHEN** only `fix.title.update` is set and a request fixes a vulnerability
- **THEN** the vulnerability request keeps the default title, for example `Deepward: fix vulnerabilities: lz4-java 1.11.2 → 1.11.4`

### Requirement: Title placeholders
Title patterns SHALL support `{package}` (full package name), `{name}` (short name: the part after the last `:` or `/`), `{from}` and `{to}` (versions), `{count}` (number of package changes), `{severity}` (highest severity of the fixed findings), `{project}` (project directory, empty at the repository root), `{ecosystem}`, `{group}` (group name) and `{update}` (patch, minor or major). Patterns SHALL NOT support conditions or loops.

#### Scenario: Group request
- **WHEN** `fix.title.group` is `chore(deps): update {group} ({count} packages)` and the `jackson` group rule collects three package changes
- **THEN** the title is `chore(deps): update jackson (3 packages)`

#### Scenario: Placeholders without a value
- **WHEN** a `group` pattern uses `{from}` and `{to}`
- **THEN** they render empty, and the surrounding whitespace is collapsed

### Requirement: Empty project in titles
When `{project}` is empty, the system SHALL remove it together with brackets left empty around it and a preposition directly before it (`in`, `for`, `at`), and SHALL collapse repeated spaces.

#### Scenario: Scope in brackets at the root
- **WHEN** the pattern is `fix({project}): {name} {from} → {to}` and the project is at the repository root
- **THEN** the title is `fix: kafka-clients 4.4.0 → 4.5.0`

#### Scenario: Preposition at the root
- **WHEN** the pattern is `Update {name} {from} → {to} in {project}` and the project is at the repository root
- **THEN** the title is `Update grpc-stub 1.85.1 → 1.86.0`

#### Scenario: Project outside the root
- **WHEN** the same pattern is used for a project in `services/api`
- **THEN** the title is `Update grpc-stub 1.85.1 → 1.86.0 in services/api`

### Requirement: Markers in custom titles
Whatever the pattern, the system SHALL put `[unverified] ` in front of the title of a request with unverified changes and append ` (not built)` to the title of a request whose findings are all in packages that are not built.

#### Scenario: Unverified pin
- **WHEN** `fix.title.pin` is `fix(deps): pin {name} to {to}` and the pin of `lz4-java` to 1.11.4 is published as unverified
- **THEN** the title is `[unverified] fix(deps): pin lz4-java to 1.11.4`

### Requirement: Commit subject follows the title
When the title of a request comes from a pattern, the system SHALL use the rendered title, markers included, as the subject of its commit. Without a pattern, the commit subject SHALL stay the default title without the `Deepward: ` prefix.

#### Scenario: Conventional commit
- **WHEN** a request is titled `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0` from a pattern
- **THEN** its commit subject is `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0`

### Requirement: Title pattern validation
The system SHALL reject, when loading the configuration, an unknown key under `fix.title` and an unknown placeholder in a pattern, naming the key and the placeholder. A pattern that renders an empty title SHALL be rejected. Rendered titles SHALL be cut to 255 characters.

#### Scenario: Unknown placeholder
- **WHEN** `fix.title.update` is `chore(deps): bump {version}`
- **THEN** configuration validation fails with an error naming `fix.title.update` and `{version}`

#### Scenario: Unknown kind
- **WHEN** `fix.title` contains a key `bugfix`
- **THEN** configuration validation fails with an error naming the unknown key

#### Scenario: Pattern without text
- **WHEN** `fix.title.security` is `{project}` and the project is at the repository root
- **THEN** configuration validation rejects the pattern because it can render an empty title

### Requirement: Affected modules in the change request
When the findings a change request fixes come from a project with modules, its description SHALL list, for each package change, the modules those findings affect. Change requests of projects without modules SHALL keep their current description.

#### Scenario: Pin affecting two modules
- **WHEN** a change request pins `lz4-java` to 1.11.4 and the fixed findings list modules `wr-history-api` and `wr-history-app`
- **THEN** the description names `wr-history-api` and `wr-history-app` for that change

### Requirement: NuGet pins in the entering modules
In a NuGet project with modules, a pin of a transitive package SHALL be a `PackageVersion` in `Directory.Packages.props` when Central Package Management pins transitively (`CentralPackageTransitivePinningEnabled`). Otherwise it SHALL add a `PackageReference` to each module through which the package enters the project from outside, that is, each module that reaches it without passing through another module.

#### Scenario: Pin in the module that brings the package
- **WHEN** `Web` and `Api` reference `Core`, `Core` brings vulnerable `System.Text.Json` through a package, and transitive pinning is off
- **THEN** the change request adds a `PackageReference` for `System.Text.Json` only to `Core.csproj`

#### Scenario: Central transitive pinning
- **WHEN** `Directory.Packages.props` sets `CentralPackageTransitivePinningEnabled` to `true`
- **THEN** the change request adds only a `PackageVersion` to `Directory.Packages.props`

#### Scenario: One fix for the whole solution
- **WHEN** a vulnerable package is reached by thirty project files of one CPM project
- **THEN** one change request fixes it, editing `Directory.Packages.props` once

### Requirement: Requests of merged projects are closed
When an open bot request belongs to a project that is no longer detected and whose directory now lies inside a scanned project of the same ecosystem, the system SHALL close it with a comment naming the project that replaces it. Requests of projects that are missing for other reasons SHALL stay open.

#### Scenario: Project file became a module
- **WHEN** a request for project `nuget:src/Web` is open and `src/Web/Web.csproj` is now a module of `nuget:.`
- **THEN** the request is closed with a comment saying its project is now part of `nuget:.`

#### Scenario: Project removed from the repository
- **WHEN** a request belongs to project `nuget:legacy` and no scanned project contains `legacy/`
- **THEN** the request stays open


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
