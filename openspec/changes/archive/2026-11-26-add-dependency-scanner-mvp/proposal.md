# Proposal

## Why

Our sbt, Maven and NuGet projects need a tool that finds vulnerable and outdated dependencies, transitive ones included, and can optionally open MRs/PRs. Renovate (AGPL) and Dependabot do not fit: sbt support is weak, transitive version pinning is missing, there is no unified vulnerability report, and they are hard to embed into an arbitrary CI. Beyond dependencies, teams already run separate checkers (linters, static analyzers, secret scanners), each with its own report, gate and suppression format. We need one CLI that runs in any CI, is not tied to a particular git hosting, and gives a single report, quality gate and suppression policy across all checks.

**Positioning.** `deepward` is a universal **checks orchestrator**. Dependency analysis is its built-in, first-class plugin set and the whole scope of this MVP. Other kinds of checks (static analysis, linters, secret scanning) join by integrating existing external tools through configuration. Deepward does not implement its own static analyzers.

## What Changes

- New `deepward` CLI: a single static Go binary for Linux, macOS and Windows.
- Plugin architecture: ecosystems, analyzers, advisory sources, reporters and hosting platforms are plugins behind stable contracts, sharing one SARIF-compatible finding model. All built-in features ship as plugins, so new checks are added by implementing a contract or by configuring an external tool. External tools can already contribute findings through SARIF import and config-defined integrations.
- Built-in dependency plugin set ("Deepward deps"): the ecosystems, analyzers and advisory sources below. It is the only analysis code shipped in this MVP.
- Toolchain extension points: plugins declare the external tools they need, and a configurable resolver chain locates them (explicit path, project wrapper, `PATH`, opt-in container image or checksum-verified download). Per-tool arguments, env and JVM options, build-tool add-ons (Maven plugin goals, injected sbt plugin files, dotnet local tools), setup steps, and config-only integrations (run any CLI, parse SARIF/CycloneDX output) are supported, along with `deepward doctor` for diagnostics.
- Full dependency graph (direct and transitive) produced by the real build tool (sbt, Maven, `dotnet`) and normalized into one purl-based model.
- Vulnerability analysis against open sources whose data license allows commercial use: OSV.dev (including GHSA) and the GitHub Advisory Database, with an offline mode backed by a local database copy.
- New-version discovery in public and private registries (Maven Central, Nexus, Artifactory, NuGet v3).
- Reports: a self-contained HTML file (no external resources), machine-readable JSON, and SARIF for CI integration.
- Optional, explicitly requested creation and updating of GitLab MRs and GitHub PRs using deterministic branches. By default the tool writes nothing to the repository.
- In-repository configuration (`deepward.yaml`): ignore lists, severity threshold, grouping rules, open MR limit.
- Exit codes suitable for a CI quality gate (for example, fail on high and above).

## Capabilities

### New Capabilities
- `plugin-framework`: plugin kinds and contracts, plugin configuration, shared facts, the generic finding and remediation model, fault isolation, SARIF import of external results.
- `toolchain`: declaring, resolving, provisioning, configuring and running external tools and build-tool add-ons, setup steps, config-defined command integrations, and toolchain diagnostics.
- `dependency-graph`: detecting sbt, Maven and NuGet projects in a repository and building a normalized dependency graph via the native build tool.
- `vulnerability-analysis`: matching the graph against vulnerability databases, deduplicating findings across sources, computing severity and fixed versions, applying ignore rules.
- `update-discovery`: finding available versions in registries with ecosystem-specific versioning rules, Scala cross-version suffixes and prerelease handling.
- `scan-report`: producing HTML, JSON and SARIF reports from analysis results.
- `change-requests`: creating, updating and closing GitLab MRs and GitHub PRs for updates and vulnerability fixes.
- `cli`: commands, configuration, environment variables, exit codes and CI behavior.

### Modified Capabilities
<!-- None: the project has no specs yet. -->

## Impact

- New Go module at the repository root, with packages laid out as DDD modules (see design.md).
- The project itself is licensed under MIT (`LICENSE` at the repository root).
- External dependencies use permissive licenses only (MIT, BSD, Apache 2.0). The binary contains no copyleft (GPL/AGPL) code. Vulnerability data licenses are checked separately from code licenses.
- The runtime environment must provide the build tools (sbt, mvn, dotnet). The tool invokes them but does not install them.
- Network access to registries and vulnerability sources is required, or a local mirror for offline mode.
- MR/PR mode needs a GitLab or GitHub token with write access to the repository.
- Out of MVP scope: auto-merge, an issue dashboard, Gradle/npm and other ecosystems, automatic transitive pinning for sbt. Pinning covers Maven and NuGet only (see design.md).
