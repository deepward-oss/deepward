## ADDED Requirements

### Requirement: npm registry versions
The system SHALL read npm package versions from the npm registry protocol (default `https://registry.npmjs.org`). Registries can be configured per scope and authenticated with a token or user and password from environment variables. Deprecated versions SHALL NOT be proposed as updates.

#### Scenario: Scoped private registry
- **WHEN** `@acme` is mapped to `https://npm.acme.example` with `tokenEnv: ACME_NPM_TOKEN`
- **THEN** versions of `@acme/ui` come from that registry with a bearer token, and other packages come from the default registry

#### Scenario: Deprecated release
- **WHEN** version 2.1.0 is deprecated in the registry
- **THEN** 2.1.0 is not proposed

### Requirement: Go module proxy versions
The system SHALL read Go module versions from the module proxy protocol, using the proxies from `GOPROXY` (default `https://proxy.golang.org`). Modules matching `GOPRIVATE` or `GONOPROXY` SHALL NOT be sent to a proxy, and their updates are unknown without a warning. Only versions of the same module path are proposed, so a major version with a new import path (`/v2`) is never an automatic update.

#### Scenario: Private module
- **WHEN** `GOPRIVATE=git.acme.example/*` and the project requires `git.acme.example/lib`
- **THEN** no request for that module is sent to any proxy

#### Scenario: Case-encoded module path
- **WHEN** the module is `github.com/BurntSushi/toml`
- **THEN** the proxy is asked for `github.com/!burnt!sushi/toml/@v/list`

### Requirement: crates.io versions
The system SHALL read crate versions from a sparse registry index (default crates.io). Yanked versions SHALL NOT be proposed. Crates from git or other sources than the configured registries have unknown updates.

#### Scenario: Yanked release
- **WHEN** the index marks `regex` 1.5.5 as yanked
- **THEN** 1.5.5 is not proposed

### Requirement: Semantic versioning rules for the new ecosystems
npm, Go and crates.io versions SHALL be compared by Semantic Versioning, ignoring a leading `v` and build metadata. A version with a prerelease part, including Go pseudo-versions, is a prerelease.

#### Scenario: Go pseudo-version
- **WHEN** the current version is `v0.0.0-20180917221912-90fa682c2a6e` and v0.1.0 is published
- **THEN** v0.1.0 is proposed as an update
