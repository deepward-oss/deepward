# Tasks

## 1. Repository scaffolding

- [x] 1.1 Initialize the Go module, the `cmd/deepward`, `pkg/sdk`, `internal/*` and `plugins/*` directory layout from design D2, a `Makefile` with `build`, `test`, `lint`, `cover` and `integration` targets, and verify `make build` produces a binary
- [x] 1.2 Add golangci-lint config (including `depguard` rules from D2) and a CI pipeline running lint, unit tests, the coverage gate (≥ 90% overall) and `go-licenses check` with the permissive allowlist, and verify the pipeline passes on the empty skeleton
- [x] 1.3 Add the architecture test `internal/archtest` that fails on forbidden import edges (engine → plugins, plugin → plugin, domain → I/O packages), and verify it fails on a deliberately bad import and passes after removing it

## 2. Plugin SDK (`pkg/sdk`)

- [x] 2.1 Implement value types `PURL` (with Scala base name and binary suffix), `Ecosystem`, `Severity`, `Clock`, and `Version` backed by osv-scalibr `semantic` for Maven and NuGet, and verify with table and property tests covering qualifiers, prereleases and `_2.13`, `_3` and `_2.12_1.0` suffixes
- [x] 2.2 Define the plugin contracts (`Plugin`, `Ecosystem` + `GraphProvider`/`VersionRegistry`/`ManifestEditor`, `Analyzer`, `AdvisorySource`, `Reporter`, `Platform`) and `sdk.Register`, and verify registration rejects duplicate and non-kebab-case IDs in unit tests
- [x] 2.3 Implement the typed fact keys, the dependency-graph fact type (nodes, edges, scope, direct flag, paths) and the source-tree fact, and verify path computation and graph invariants with unit tests
- [x] 2.4 Implement the generic finding and remediation model and `ScanResult` with `schemaVersion: 1` and stable ordering, and verify lossless SARIF mapping with round-trip tests
- [x] 2.5 Implement the conformance kit `pkg/sdk/sdktest` per plugin kind and in-memory fakes for every kind that pass it, and verify the fakes pass the kit
- [x] 2.6 Document the SDK in `docs/plugins.md` (contracts, facts, registration, building a custom binary, conformance kit), and verify the example plugin in the doc compiles as `examples/hello-analyzer` and passes `sdktest`

## 3. Engine (`internal/engine`)

- [x] 3.1 Implement the plugin registry and config binding of `plugins.<id>` sections against plugin schemas (enabled flag, unknown ID error, invalid value error naming the key), and verify with unit tests for each plugin-framework configuration scenario
- [x] 3.2 Implement the fact store with lazy, memoized, at-most-once-per-project production and `fact-unavailable` skip reasons, and verify with fake producers counting invocations
- [x] 3.3 Implement the scan pipeline (detect → facts → concurrent analyzers → ignore rules → gate → reporters) with per-call timeout and `recover()`, and verify with fake plugins that panic, time out and succeed
- [x] 3.4 Implement ignore rules (advisory ID plus optional purl, mandatory reason, expiry with warning) and quality gate evaluation, and verify the active and expired rule scenarios with a fixed clock

## 4. CLI skeleton (`internal/cli`)

- [x] 4.1 Implement cobra commands `scan`, `report`, `fix`, `db update`, `plugins list`, `config validate`, `version` with `--help` and unknown-command suggestions, and verify with CLI tests asserting output and exit code 2 for `deepward scna`
- [x] 4.2 Implement configuration layering (flags > `DEEPWARD_*` env > `deepward.yaml` > defaults) with `KnownFields` errors including line numbers and secret masking in `config validate`, publish the JSON Schema for `deepward.yaml`, and verify each cli configuration scenario in tests
- [x] 4.3 Implement exit codes 0/1/2/3 with `--allow-partial`, output channels (summary to stdout, logs to stderr, `--log-format json`, `--quiet`, `NO_COLOR`, no ANSI when not a TTY), and verify with E2E tests on fake plugins
- [x] 4.4 Implement CI detection for GitLab CI and GitHub Actions plus generic flags (`--repo-url`, `--commit`, `--target-branch`), and verify with env-driven table tests
- [x] 4.5 Write `docs/cli.md` and `docs/configuration.md`, and verify every documented command and config example is executed by a doc test

## 5. Toolchain (`internal/toolchain`)

- [x] 5.1 Implement the process runner with timeout, last-200-lines ring buffer, secret masking (including `${env:...}` references), environment allowlist and removal of `DEEPWARD_*_TOKEN`, and verify with tests using a helper test binary that echoes env and secrets
- [x] 5.2 Implement `sdk.Tools` with the tool catalog, version probes and constraint checks (`tool-not-found`, `tool-version-mismatch`), and verify with fake tools that print configurable versions
- [x] 5.3 Implement the resolver chain plus the `config-path`, `wrapper` (`mvnw`, sbt launcher script, dotnet local tools manifest) and `path` resolvers, with per-tool order and caching, and verify the wrapper-preferred and explicit-path scenarios on fixture trees
- [x] 5.4 Implement per-tool settings (args, env, javaHome, jvmOpts, workdir, timeout) merged into every invocation, and verify the corporate settings scenario by asserting the exact argv and env passed to a fake tool
- [x] 5.5 Implement the opt-in `container` (docker/podman, host UID/GID, same mount path) and `download` (SHA-256 required, cache) resolvers, and verify the missing-checksum scenario, a checksum mismatch, and the argv built for the container
- [x] 5.6 Add the archtest rule forbidding `os/exec` outside `internal/toolchain`, and verify it fails on a deliberate violation in a plugin package
- [x] 5.7 Document tools, resolvers and settings in `docs/toolchain.md`, and verify the documented examples load via `deepward config validate`

## 5a. Add-ons, setup steps, integrations and doctor

- [x] 5a.1 Implement `AddonSpec` in the SDK and add-on resolution (defaults overlaid with `ecosystems.<id>.addons`), and verify with a fake ecosystem that overridden and added add-ons reach its invocation
- [x] 5a.2 Implement setup steps per ecosystem and per project, run before fact producers, with failures scoped to matching projects, and verify the `dotnet tool restore` scenario and failure scoping with fake tools
- [x] 5a.3 Implement the `sarif` and `cyclonedx` output-format plugins, and verify they pass the conformance kit with fixture files
- [x] 5a.4 Implement `internal/integrations` compiling `integrations.<id>` into analyzer and fact-producer plugins (templated args, output file, format lookup, unknown-format error), and verify the semgrep-style and CycloneDX-producer scenarios with fake tools emitting fixture output
- [x] 5a.5 Implement `deepward doctor` (requirements of enabled plugins and integrations → resolution table, exit code 3 when anything is unresolved), and verify the missing-sbt scenario in a CLI test
- [x] 5a.6 Document add-ons, setup steps and integrations with worked examples (semgrep, cyclonedx-maven-plugin, CycloneDX dotnet tool) in `docs/integrations.md`, and verify the examples pass `deepward config validate`

## 6. Maven ecosystem plugin

- [x] 6.1 Implement project detection (reactor root, excluded dirs) and verify the monorepo, nested-modules and excluded-directory scenarios on fixture trees
- [x] 6.2 Implement the `GraphProvider` via the `dependency-plugin` add-on (`tree -DoutputType=json`, default 3.8.1) with settings.xml passthrough and merging of reactor modules, and verify with recorded JSON fixtures, including the version-conflict, tool-not-found and build-failure scenarios
- [x] 6.3 Implement the `VersionRegistry` reading `maven-metadata.xml` from ordered repositories with retries and auth from env, and verify with an `httptest` registry, including Nexus-only and 5xx scenarios
- [x] 6.4 Run the conformance kit for the Maven plugin and document Maven specifics in `docs/ecosystems/maven.md`, and verify the kit passes

## 7. Dependency vulnerability analyzer and OSV source

- [x] 7.1 Implement the `osv` advisory source (`/v1/querybatch`, `/v1/vulns/{id}`, data license declaration) and verify with recorded OSV responses and a test that unlicensed sources are refused
- [x] 7.2 Implement `depvuln` domain logic: range matching via `sdk.Version`, deduplication by aliases, severity policy (CVSS v3/v4, fallback, max wins), fixed-version selection and direct-ancestor attribution, and verify each vulnerability-analysis scenario in unit tests
- [x] 7.3 Emit findings with kind `vulnerability`, package and manifest locations and `PackageVersionChange` remediations, and verify with an engine test on a fake ecosystem plus the conformance kit

## 8. Reporters

- [x] 8.1 Implement the `json` reporter and `deepward report --from`, and verify golden files and byte-identical output across two runs with a fixed clock
- [x] 8.2 Implement the `html` reporter (single file, embedded CSS/JS, summary by severity/project/analyzer, vulnerability and outdated views, generic findings view, skipped/failed section, suppressed section, filters/sort/search, no-JS readability, data attribution footer), and verify golden files, a test asserting no external URLs in `src`/`href`, and the XSS scenario
- [x] 8.3 Implement the `sarif` reporter with manifest-file locations and tool attribution, and verify golden files and validation against the official SARIF 2.1.0 schema
- [x] 8.4 Verify end to end: `deepward scan` on the Maven fixture with a recorded OSV server writes HTML, JSON and SARIF whose severity counts agree

## 9. Outdated-dependencies analyzer

- [x] 9.1 Implement update classification (latest stable, patch/minor/major), prerelease rules, purl-pattern rules (ignore, `maxUpdate`, range pin, allow prerelease) and Scala cross-version lookup, and verify each update-discovery scenario in unit tests
- [x] 9.2 Implement the disk metadata cache with TTL and `--no-cache`, and verify with a counting `httptest` registry that a repeated run issues no requests
- [x] 9.3 Emit findings with kind `outdated` and remediations, and verify the analyzer passes the conformance kit and appears in the HTML outdated view

## 10. sbt ecosystem plugin

- [x] 10.1 Write the embedded sbt plugin (Scala source in `embed`, exposed as the built-in `graph-dump` add-on) that dumps the `updateFull` report per configuration and Scala version as JSON, injected via `--addPluginSbtFile`, and verify with recorded JSON fixtures parsed into the graph fact, including the cross-suffix and test-scope scenarios
- [x] 10.2 Implement detection and the `VersionRegistry` (reusing Maven metadata access through the SDK, with sbt plugin suffix handling), and verify with fixture trees and an `httptest` registry
- [x] 10.3 Run the conformance kit and document sbt specifics (Coursier cache, startup cost) in `docs/ecosystems/sbt.md`, and verify the kit passes

## 11. NuGet ecosystem plugin

- [x] 11.1 Implement detection (`*.csproj`, `*.fsproj`, `*.sln`, `Directory.Packages.props`) and the `GraphProvider` via `dotnet restore` plus `obj/project.assets.json` parsing per target framework with `nuget.config` passthrough, and verify with recorded assets fixtures
- [x] 11.2 Implement the `VersionRegistry` over the NuGet v3 flat container with SemVer 2.0 prereleases, and verify with an `httptest` feed
- [x] 11.3 Run the conformance kit and document NuGet specifics in `docs/ecosystems/nuget.md`, and verify the kit passes

## 12. Additional fact producers and advisory sources

- [x] 12.1 Implement the CycloneDX SBOM import fact producer (`--sbom <project>=<path>`, spec 1.4+) that bypasses the build tool, and verify the SBOM scenario with a fixture and an assertion that no process was spawned
- [x] 12.2 Implement the `osv-offline` source and `deepward db update` (per-ecosystem zip export, ETag), and verify the offline and missing-database scenarios with network access disabled in the test
- [x] 12.3 Implement the `ghsa` source with CC-BY-4.0 attribution and `sources.ghsa.enabled`, and verify deduplication with OSV on recorded responses
- [x] 12.4 Add a data-license review checklist for advisory sources to `docs/data-licenses.md`, and verify every registered source is listed in it by a doc test

## 13. SARIF import analyzer

- [x] 13.1 Implement `sarif-import` (`--import-sarif <tool>=<path>`, validation, mapping into findings with source tool) and verify the external-linter and invalid-file scenarios, including the generic HTML view

## 14. Fix pipeline and manifest editors

- [x] 14.1 Implement `internal/textedit` span edits that preserve line endings and BOM, and verify with property and fuzz tests that only the targeted span changes
- [x] 14.2 Implement the Maven `ManifestEditor` (literal `<version>`, `${property}` resolution across repo POMs, `dependencyManagement` pinning with an advisory comment, `manual` for external parents), and verify each change-requests Maven scenario on fixture POMs
- [x] 14.3 Implement the sbt `ManifestEditor` (Scala-aware tokenizer, `%%`/`%` patterns, shared `val` literals, `manual` on ambiguity, `dependencyOverrides` recommendation text), and verify the shared-val and transitive-sbt scenarios plus fuzzing of the tokenizer
- [x] 14.4 Implement the NuGet `ManifestEditor` (`PackageReference`, `PackageVersion` in `Directory.Packages.props`, transitive pinning), and verify on fixture project files
- [x] 14.5 Implement the engine fix pipeline: collect remediations, group by rules and shared version tokens, bounded ancestor trial (default 5 candidates) with verification by graph-fact recomputation and caching by manifest hash, dry-run unified diff by default, and verify with fake ecosystems including the `verification-failed` scenario

## 15. Hosting platform plugins

- [x] 15.1 Implement the deterministic branch naming, the hidden bot marker, the MR/PR description builder (change table, advisories, release-note links) and the limit policy with the critical/high bypass in the engine, and verify the grouping, limit and content scenarios with a fake platform
- [x] 15.2 Implement the `gitlab` platform (API v4, self-managed URL, Commits API multi-file actions, MR find/create/update/close/comment, labels, human-commit detection) and verify against an `httptest` GitLab replaying recorded responses
- [x] 15.3 Implement the `github` platform (REST v3/Enterprise, Git Data API commits, PR operations, labels, human-commit detection) and verify against an `httptest` GitHub replaying recorded responses
- [x] 15.4 Implement `fix --publish` guards (no token → config error before any call, no writes without `--publish`), and verify with a fake platform recording zero write calls
- [x] 15.5 Document publishing and recommended CI setups (separate write-token job) in `docs/publishing.md`, and verify the sample GitLab CI and GitHub Actions snippets lint cleanly

## 16. Distribution

- [x] 16.1 Add release builds for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with version, commit and date injected, plus a minimal container image with only the binary, and verify `deepward version` in the image prints them

## 17. Integration checks

- [x] 17.1 Add `//go:build integration` tests running real Maven, sbt and .NET SDK in Docker against sample projects in `testdata/projects/` (including a known vulnerable dependency per ecosystem), wire them into CI, and verify each produces the expected finding
- [x] 17.2 Add an E2E test running the built binary against a fixture monorepo with recorded OSV, registry and GitLab servers through `scan` and `fix --publish`, and verify exit codes, the three reports and the exact sequence of platform API calls
- [x] 17.3 Verify the coverage gates (≥ 95% for domain/app packages, ≥ 85% for adapters, ≥ 90% overall), the architecture test and the license check all pass in CI

## Workflow follow-up

- Pilot `deepward scan` in report-only mode on one repository per ecosystem before enabling `fix --publish`.
- Archive the change after review and pilot feedback.
