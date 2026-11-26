## Purpose

Prepare manifest edits that update dependencies and fix vulnerabilities, and, on explicit request, publish them as GitLab Merge Requests or GitHub Pull Requests.

## ADDED Requirements

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
