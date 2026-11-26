## ADDED Requirements

### Requirement: Go module scopes from compiled packages
The system SHALL give each Go module node the scope `compile` if a package of it is built by the module's packages, `test` if only their tests need it, and `not-built` otherwise. Builds SHALL be evaluated for the configured target platforms (default linux/amd64, darwin/arm64, windows/amd64), and a module built for any of them is built.

#### Scenario: Driver declared but not imported
- **WHEN** the service imports only the pgx and iofs drivers of `golang-migrate/migrate/v4`, and the module graph also contains `github.com/docker/docker` through migrate
- **THEN** the `golang-migrate/migrate/v4` node has scope `compile` and the `github.com/docker/docker` node has scope `not-built`

#### Scenario: Test-only module
- **WHEN** `github.com/alicebob/miniredis/v2` is imported only by `_test.go` files
- **THEN** its node has scope `test`

#### Scenario: Windows-only dependency
- **WHEN** a module is imported only by files with the `windows` build constraint
- **THEN** its node has scope `compile`

### Requirement: Conservative fallback for Go scopes
If the compiled package set cannot be determined, every Go module node SHALL keep scope `compile`, and the scan SHALL continue without hiding any module.

#### Scenario: Package listing fails
- **WHEN** `go list -deps` exits non-zero for every platform
- **THEN** all module nodes have scope `compile` and the project is scanned
