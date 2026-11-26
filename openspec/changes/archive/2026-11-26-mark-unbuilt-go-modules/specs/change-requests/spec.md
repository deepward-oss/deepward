## ADDED Requirements

### Requirement: Fixes for packages that are not built
Findings marked not built SHALL be fixed like other findings, for example a Go module pinned with `go get`. A change request whose findings are all not built SHALL say so in its title and description, SHALL be sorted after change requests for built packages, and SHALL NOT bypass the open request limit, whatever its severity.

#### Scenario: High advisory in a module that is not built
- **WHEN** the only finding of a change request is a high advisory on `google.golang.org/grpc`, marked not built, and the open request limit is reached
- **THEN** the change request is deferred, and its title ends with "(not built)"
