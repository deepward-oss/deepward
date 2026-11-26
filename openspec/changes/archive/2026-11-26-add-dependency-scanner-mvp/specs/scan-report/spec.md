## Purpose

Produce reports from scan results: a self-contained HTML report for people, and JSON and SARIF for machines and CI integration.

## ADDED Requirements

### Requirement: Self-contained HTML report
The system SHALL generate the HTML report as a single file that loads no external resources (scripts, styles, fonts and images are inlined), so it can be opened as a CI artifact without network access.

#### Scenario: Opening the report offline
- **WHEN** `deepward-report.html` is opened in a browser with no network
- **THEN** the report renders fully and the browser makes no network requests

### Requirement: HTML report content
The HTML report SHALL contain a summary (finding counts by severity, by project, by analyzer, and by direct vs transitive where applicable), one section per finding kind, a list of skipped or failed projects and plugins, and a suppressed-findings section. Kinds `vulnerability` and `outdated` get dedicated tables. Any other kind is rendered by a generic findings table.

#### Scenario: Finding kind without a dedicated view
- **WHEN** the result contains findings of kind `code-issue` imported from SARIF
- **THEN** the report shows them in a generic table with rule, severity, message and file location, and they are counted in the summary

#### Scenario: Severity summary
- **WHEN** 2 critical, 5 high and 1 medium findings exist
- **THEN** the report header shows these counts and they match the vulnerability table rows per severity

#### Scenario: Finding details
- **WHEN** the user expands a vulnerability row
- **THEN** it shows identifiers and aliases linked to their source, description, CVSS, affected versions, fixed version, the dependency path from root to package, and the recommended action

### Requirement: HTML report interactivity
The HTML report SHALL support filtering by project, ecosystem, severity and dependency type (direct or transitive), column sorting, and text search by package name and advisory ID. Without JavaScript it SHALL remain readable as static tables.

#### Scenario: Filtering by project
- **WHEN** project `services/api` is selected in the filter
- **THEN** tables and counters show only that project's findings

### Requirement: Escaping external data
All strings from external sources (advisory descriptions, package names, build tool output) MUST be escaped before insertion into HTML, so the report never executes markup or scripts from that data.

#### Scenario: Malicious advisory description
- **WHEN** an advisory description contains `<script>alert(1)</script>`
- **THEN** the text is shown literally and the script does not run

### Requirement: Machine-readable JSON
The system SHALL emit a JSON report following a versioned schema (`schemaVersion`) with all projects, graphs, findings, updates, warnings and run metadata (tool version, time, sources, commit). The HTML report SHALL be rendered from the same data as the JSON.

#### Scenario: HTML and JSON agree
- **WHEN** both reports are generated from one run
- **THEN** finding counts per severity in the JSON equal those in the HTML

#### Scenario: Re-rendering HTML from JSON
- **WHEN** `deepward report --from result.json --format html` is run
- **THEN** the HTML is produced without rescanning

### Requirement: SARIF for CI
The system SHALL emit a SARIF 2.1.0 report in which each vulnerability is a result located at the manifest file declaring the direct dependency, compatible with GitHub code scanning and GitLab.

#### Scenario: Upload to GitHub code scanning
- **WHEN** the SARIF file is uploaded with `github/codeql-action/upload-sarif`
- **THEN** the upload passes schema validation and results carry file paths relative to the repository root

### Requirement: Deterministic reports
Given identical inputs (graphs, source responses, clock), the JSON and SARIF reports SHALL be byte-identical, with stable element ordering.

#### Scenario: Two runs on fixtures
- **WHEN** the report is generated twice from the same dataset with a fixed clock
- **THEN** the JSON files are byte-identical
