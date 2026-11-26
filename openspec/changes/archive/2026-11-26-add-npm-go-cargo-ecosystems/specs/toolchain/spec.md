## ADDED Requirements

### Requirement: Built-in tools for the new ecosystems
The toolchain SHALL know `npm`, `yarn`, `pnpm`, `go` and `cargo`, including their executable names on Windows and how to read their versions, so they can be resolved, configured with `tools.<name>` and checked by `deepward doctor` without extra configuration.

#### Scenario: Doctor on a pnpm project
- **WHEN** `deepward doctor` runs in a repository with `pnpm-lock.yaml` and pnpm 9.15.0 is on `PATH`
- **THEN** the output lists `pnpm` with version 9.15.0 and the resolver that found it

### Requirement: Package managers are only needed to change files
Scans of Node.js and Cargo projects with a committed lockfile SHALL NOT require the package manager. It is required only to generate a missing lockfile and to realize fixes.

#### Scenario: Scan without Node.js installed
- **WHEN** a project has `package-lock.json` and `npm` is not available
- **THEN** the project is scanned successfully
