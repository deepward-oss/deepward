## Purpose

Provide one extensible mechanism for locating, provisioning, configuring and running the external tools that plugins depend on (build tools such as mvn, sbt and dotnet, their add-ons such as Maven plugins, sbt plugins and dotnet tools, and arbitrary third-party CLIs), so new tools are added through configuration or a resolver plugin rather than plugin-specific code.

## ADDED Requirements

### Requirement: Declared tool requirements
Plugins SHALL declare the external tools they need as requirements (logical tool name, optional version constraint, purpose) and SHALL obtain a runnable tool only from the toolchain. Plugins MUST NOT spawn processes directly. A requirement that cannot be satisfied makes the dependent work skipped with reason `tool-not-found` or `tool-version-mismatch`.

#### Scenario: Version constraint not met
- **WHEN** the sbt ecosystem requires `sbt >=1.4` and the resolved sbt reports 1.3.13
- **THEN** sbt projects are skipped with reason `tool-version-mismatch: sbt 1.3.13 does not satisfy >=1.4`

### Requirement: Ordered tool resolution
The system SHALL resolve each tool through an ordered chain of resolvers. Default order: explicit configured path, project-local wrapper (`mvnw`, `sbtw`/`sbt` launcher script, local `dotnet` tools manifest), then `PATH`. The first resolver whose candidate satisfies the version constraint wins. The order SHALL be configurable per tool.

#### Scenario: Maven wrapper preferred
- **WHEN** a Maven project contains `mvnw` and `mvn` 3.6.3 is also on `PATH`
- **THEN** the project's `mvnw` is used and the report records resolver `wrapper` and the wrapper's Maven version

#### Scenario: Explicit path overrides discovery
- **WHEN** configuration sets `tools.dotnet.path: /opt/dotnet-8/dotnet`
- **THEN** that binary is used for all NuGet projects regardless of `PATH`

### Requirement: Opt-in container and download resolvers
The system SHALL provide a `container` resolver that runs a tool inside a configured image (docker or podman) with the workspace mounted, and a `download` resolver that fetches a tool archive from a configured URL, verifies its SHA-256 checksum and caches it. Both MUST be disabled unless explicitly configured, and a download without a checksum MUST be refused.

#### Scenario: sbt from a container image
- **WHEN** `tools.sbt.resolvers: [container]` and `tools.sbt.image: sbtscala/scala-sbt:eclipse-temurin-17.0.10_7_1.10.1_2.13.14` are configured
- **THEN** sbt commands run inside that image with the project mounted, and graph results are the same as with a local sbt

#### Scenario: Download without a checksum
- **WHEN** `tools.semgrep.download.url` is set but `sha256` is missing
- **THEN** the run exits with the configuration error code before downloading anything

### Requirement: Per-tool invocation settings
For every tool, configuration SHALL allow extra arguments, environment variables (literal or referenced from the CI environment as secrets), JVM options where relevant (`MAVEN_OPTS`, `SBT_OPTS`, `JAVA_HOME`), a working-directory override and a timeout. These settings SHALL apply to every invocation of that tool by any plugin.

#### Scenario: Corporate Maven settings and JVM options
- **WHEN** `tools.mvn.args: ["-s", "ci/settings.xml", "-Dmaven.repo.local=.m2"]` and `tools.mvn.env.MAVEN_OPTS: "-Xmx2g"` are configured
- **THEN** every mvn invocation includes those arguments and that environment

### Requirement: Build-tool add-ons
Ecosystem plugins SHALL expose add-on extension points: Maven plugin goals by full coordinates with configurable versions, additional sbt plugin files injected with `--addPluginSbtFile`, and dotnet local tools restored from a tool manifest. Configuration SHALL be able to override the version of every add-on the built-in plugins use, and to add new add-ons, without modifying the project's build files.

#### Scenario: Pinning the dependency plugin version
- **WHEN** configuration sets `ecosystems.maven.addons.dependency-plugin.version: 3.6.1` because the corporate mirror lacks 3.8.1
- **THEN** the graph is built with `maven-dependency-plugin:3.6.1`

#### Scenario: Extra sbt plugin file
- **WHEN** `ecosystems.sbt.addons.pluginFiles: [ci/corp-resolvers.sbt]` is configured
- **THEN** every sbt invocation injects that file alongside the built-in one, and the repository's `project/` directory is not modified

### Requirement: Setup steps
Configuration SHALL allow setup commands that run before graph production, per ecosystem or per project (for example `dotnet tool restore` or installing internal modules into the local Maven repository). Setup steps use the same tool resolution and invocation settings. A failed setup step fails only the projects it applies to.

#### Scenario: Restore dotnet tools first
- **WHEN** `ecosystems.nuget.setup: [{tool: dotnet, args: [tool, restore]}]` is configured
- **THEN** `dotnet tool restore` runs once per NuGet project before `dotnet restore`

### Requirement: Config-defined command integrations
The system SHALL let configuration define new integrations backed by any external tool without writing code: a command analyzer (run a tool, parse its output in a registered output format such as SARIF, into findings) and a command fact producer (run a tool, parse its output, for example CycloneDX, into a fact such as `dependency-graph`). Command integrations SHALL behave like code plugins with respect to IDs, enabling, isolation, timeouts and reporting.

#### Scenario: Semgrep as a command analyzer
- **WHEN** configuration defines `integrations.semgrep: {type: analyzer, tool: semgrep, args: [scan, --sarif, --output, "{{.OutputFile}}", "{{.ProjectDir}}"], output: sarif}`
- **THEN** `deepward scan` runs semgrep per project and its results appear as findings with analyzer `semgrep`

#### Scenario: CycloneDX Maven plugin as a graph producer
- **WHEN** configuration defines a command fact producer for Maven projects running `org.cyclonedx:cyclonedx-maven-plugin:makeAggregateBom` with `output: cyclonedx` and `fact: dependency-graph`
- **THEN** the dependency graph for Maven projects comes from that SBOM instead of the built-in producer

#### Scenario: Unknown output format
- **WHEN** an integration declares `output: spdx` and no `spdx` output format plugin is registered
- **THEN** the run exits with the configuration error code naming the integration and the format

### Requirement: Toolchain diagnostics
The system SHALL provide `deepward doctor`, which resolves every tool required by enabled plugins and integrations for the scan root and prints, per tool, the resolver used, the path or image, the detected version, and whether constraints are satisfied, without running any analysis.

#### Scenario: Doctor in a CI image missing sbt
- **WHEN** `deepward doctor` runs in a repository with sbt and Maven projects on an image without sbt
- **THEN** mvn is shown as resolved with its version, sbt as `not found` with the resolvers tried, and the exit code is 3
