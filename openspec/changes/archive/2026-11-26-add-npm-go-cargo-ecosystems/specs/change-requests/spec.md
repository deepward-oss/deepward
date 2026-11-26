## ADDED Requirements

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
