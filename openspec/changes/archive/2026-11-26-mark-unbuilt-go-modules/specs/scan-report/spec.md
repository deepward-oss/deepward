## ADDED Requirements

### Requirement: Not-built findings are highlighted
Reports SHALL keep findings marked not built visibly apart from the others:
- the HTML report lists them in their own section with an explanation, a "not built" badge and a filter, and its severity cards count only built findings plus a separate "not built" card;
- the CLI summary prints their count separately;
- JSON and SARIF carry a `notBuilt` flag on each such finding, and SARIF adds the tag `not-built`.

#### Scenario: HTML report with built and not-built vulnerabilities
- **WHEN** a scan has 1 built and 16 not-built vulnerabilities
- **THEN** the "Vulnerabilities" section lists 1 finding, a "Not built" section lists 16 with an explanation of what not built means, and the summary shows a "not built: 16" card

#### Scenario: CLI summary
- **WHEN** the same scan finishes
- **THEN** the summary line counts only the built finding by severity, and a second line reads "+ 16 in packages that are not built (…by severity…), excluded from the quality gate"
