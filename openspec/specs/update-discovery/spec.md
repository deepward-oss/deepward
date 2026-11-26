# update-discovery Specification

## Purpose
Find newer available versions of dependencies in public and private registries using each ecosystem's versioning rules. Outdated-dependency reporting and update proposals build on this.

## Requirements

### Requirement: Version sources
The system SHALL read available Maven and sbt versions from `maven-metadata.xml` of the configured repositories (Maven Central, Nexus, Artifactory) and NuGet versions from the NuGet v3 API (flat container). Registries SHALL be queried in configured order, and version lists of a package found in several registries are merged.

#### Scenario: Package only in internal Nexus
- **WHEN** `com.acme:billing-client` exists only in the configured Nexus
- **THEN** versions come from Nexus and its absence from Central is not an error

#### Scenario: Registry unavailable
- **WHEN** a registry answers 5xx after three retries
- **THEN** updates for affected packages are marked `unknown`, the report contains a warning, and the scan continues

### Requirement: Update classification
For each direct dependency the system SHALL determine the latest stable version and the latest versions within the current patch, minor and major lines, classifying the update type (`patch`, `minor`, `major`) by ecosystem rules.

#### Scenario: Patch and major available
- **WHEN** the current version is 1.4.2 and the registry has 1.4.5, 1.5.0 and 2.0.1
- **THEN** the result contains `patch: 1.4.5`, `minor: 1.5.0`, `major: 2.0.1`

#### Scenario: Prereleases excluded
- **WHEN** the registry has `3.0.0-M1`, `3.0.0-RC2` and NuGet `3.0.0-preview.1`, and the current version is stable
- **THEN** these versions are not proposed unless configuration allows prereleases for that package

#### Scenario: Current version is a prerelease
- **WHEN** the current version is `2.0.0-RC1`
- **THEN** newer prereleases of the same line and stable versions are proposed

### Requirement: Scala cross versions
For Scala artifacts the system SHALL look up versions of the same base artifact with the same binary suffix (`_2.12`, `_2.13`, `_3`). For sbt plugins it SHALL respect the `_2.12_1.0` suffix.

#### Scenario: Scala library update
- **WHEN** a Scala 2.13 project uses `cats-core_2.13:2.9.0`
- **THEN** versions are looked up among `cats-core_2.13` and versions of `cats-core_3` are not proposed

### Requirement: Configured update rules
The system SHALL apply purl-pattern rules from configuration: ignoring a package, capping the update type (for example `minor`), pinning a version range, allowing prereleases.

#### Scenario: No major updates for Spring
- **WHEN** a rule for `pkg:maven/org.springframework*/*` sets `maxUpdate: minor`
- **THEN** major Spring versions are not proposed while minor and patch versions are

#### Scenario: Ignored package
- **WHEN** a rule ignores `pkg:nuget/Newtonsoft.Json`
- **THEN** its updates appear neither in the report nor in change proposals

### Requirement: Metadata caching
The system SHALL cache registry responses on disk with a configurable TTL (default 6 hours) to avoid repeated lookups within and across nearby runs. The `--no-cache` flag SHALL disable the cache.

#### Scenario: Repeated run
- **WHEN** a scan runs again 10 minutes later with the same cache directory
- **THEN** registry metadata requests are not repeated

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
