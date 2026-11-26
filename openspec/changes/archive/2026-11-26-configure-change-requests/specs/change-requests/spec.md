## ADDED Requirements

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
