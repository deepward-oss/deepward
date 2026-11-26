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
For a transitive finding the system SHALL first propose upgrading the direct ancestor to a version that brings the fix. If none exists, it SHALL pin the transitive version: Maven via `dependencyManagement`, NuGet via `PackageVersion` with `CentralPackageTransitivePinningEnabled` or an explicit `PackageReference`. sbt pinning is not performed in the MVP and the finding is marked `manual`.

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

### Requirement: Parallel verification
The system SHALL verify change groups with `fix.concurrency` workers (default 2). Each worker SHALL copy a project once, reuse the copy for every group of that project it verifies and restore the edited files between groups. The resulting plan SHALL not depend on the number of workers.

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
