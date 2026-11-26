# plugin-framework Specification

## Purpose
Make every analysis building block pluggable (ecosystems, analyzers, advisory sources, reporters, hosting platforms) behind stable contracts and one generic finding model. New kinds of analysis, including future static code analyzers, can then be added by implementing a contract, without changing the core.

## Requirements

### Requirement: Plugin kinds and contracts
The system SHALL treat ecosystems, analyzers, fact producers, advisory sources, reporters, hosting platforms, tool resolvers and output formats as plugins of distinct kinds, each implementing a published contract. The core run pipeline MUST NOT reference any concrete plugin. Every built-in feature in this change is delivered as a plugin.

#### Scenario: Adding an ecosystem without core changes
- **WHEN** a developer implements the ecosystem contract for a new build tool and registers it
- **THEN** its projects are detected, analyzed and reported by the existing pipeline with no change to core packages

#### Scenario: Built-ins are plugins
- **WHEN** `deepward plugins list` runs
- **THEN** it lists at least the ecosystems `maven`, `sbt`, `nuget`, the analyzers `dependency-vulnerabilities`, `outdated-dependencies` and `sarif-import`, the advisory sources `osv`, `osv-offline` and `ghsa`, the reporters `html`, `json` and `sarif`, the platforms `gitlab` and `github`, the tool resolvers `config-path`, `wrapper`, `path`, `container` and `download`, and the output formats `sarif-format` and `cyclonedx-format` (format names `sarif` and `cyclonedx`), each with its kind, version and enabled state, plus any config-defined integrations marked as such

#### Scenario: Adding an output format
- **WHEN** a developer implements the output-format contract for `spdx` and registers it
- **THEN** config-defined integrations can declare `output: spdx` with no other change

### Requirement: Plugin identity and configuration
Every plugin SHALL have a unique kebab-case ID, a version and a description, and MAY declare a configuration schema. Plugin settings live under `plugins.<id>` in `deepward.yaml` and are validated against that schema. A reference to an unknown plugin ID MUST be a configuration error.

#### Scenario: Disabling a plugin
- **WHEN** configuration sets `plugins.outdated-dependencies.enabled: false`
- **THEN** that analyzer does not run and its findings section is absent from reports

#### Scenario: Invalid plugin setting
- **WHEN** `plugins.osv.timeout` is set to `"fast"` and the schema expects a duration
- **THEN** the run exits with the configuration error code and names `plugins.osv.timeout`

#### Scenario: Unknown plugin
- **WHEN** configuration contains `plugins.gradle.enabled: true` and no `gradle` plugin is registered
- **THEN** the run exits with the configuration error code and reports the unknown plugin ID

### Requirement: Facts shared between plugins
Analyzers SHALL declare the facts they need (for example `dependency-graph` or `source-tree`). The pipeline SHALL compute each fact at most once per project and share it. An analyzer whose required facts are unavailable SHALL be skipped for that project with a recorded reason.

#### Scenario: Graph computed once
- **WHEN** both `dependency-vulnerabilities` and `outdated-dependencies` run on a Maven project
- **THEN** the Maven build tool is invoked once to produce the graph for both

#### Scenario: Missing fact
- **WHEN** the graph for an sbt project failed to build
- **THEN** analyzers needing `dependency-graph` are skipped for it with reason `fact-unavailable: dependency-graph`, and analyzers needing only `source-tree` still run

### Requirement: Generic finding model
Every analyzer SHALL report results as findings in one model: analyzer ID, rule ID, kind (`vulnerability`, `outdated`, `license`, `code-issue` or a plugin-defined kind), severity, message, location (a file region and/or a package purl with its dependency path), optional external references, and optional remediations. This model SHALL map losslessly to SARIF 2.1.0 results.

#### Scenario: Vulnerability finding
- **WHEN** `dependency-vulnerabilities` reports CVE-2021-44228 on `log4j-core`
- **THEN** the finding has kind `vulnerability`, rule ID `CVE-2021-44228`, a purl location with the dependency path, and the manifest file region as its file location

#### Scenario: Code-issue finding from a future static analyzer
- **WHEN** an analyzer reports kind `code-issue` with a file region `src/Main.scala:42:5`
- **THEN** reports, the quality gate, ignore rules and SARIF output handle it with no change to their code

### Requirement: Generic remediations
A finding MAY carry remediations of two forms: a package version change (project, purl, target version) or a text edit (file, byte range, replacement). The `fix` pipeline SHALL realize package version changes through the owning ecosystem's manifest editor and apply text edits directly, then verify and publish them as specified in change-requests.

#### Scenario: Remediation from any analyzer
- **WHEN** an analyzer other than the built-ins returns a text-edit remediation
- **THEN** `deepward fix` includes that edit in the change plan without analyzer-specific code in the fix pipeline

### Requirement: Fault isolation between plugins
A plugin error or panic SHALL be contained: it is recorded as a run warning with the plugin ID, and other plugins continue. Each plugin call SHALL be bounded by a configurable timeout.

#### Scenario: Analyzer panics
- **WHEN** one analyzer panics while analyzing a project
- **THEN** other analyzers' findings are still reported, the report lists the failed analyzer with the error, and the exit code follows the partial-failure rule in the cli spec

### Requirement: External results via SARIF import
The system SHALL provide a `sarif-import` analyzer that ingests SARIF 2.1.0 files produced by external tools (`--import-sarif <tool>=<path>` or configuration) and turns their results into findings, so third-party static analyzers can join reports and the quality gate.

#### Scenario: Importing results of an external linter
- **WHEN** `deepward scan --import-sarif semgrep=semgrep.sarif` runs
- **THEN** semgrep results appear in the HTML and JSON reports as findings with analyzer `sarif-import`, source tool `semgrep`, and their rule IDs, severities and locations

#### Scenario: Invalid SARIF file
- **WHEN** the imported file is not valid SARIF 2.1.0
- **THEN** the import is recorded as a failed plugin run with the validation error and other analyzers still run
