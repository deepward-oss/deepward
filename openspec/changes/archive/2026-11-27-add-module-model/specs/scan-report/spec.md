# Spec Delta

## ADDED Requirements

### Requirement: Modules in the report
For findings of projects with modules, the HTML report SHALL show in each vulnerability row's details the affected modules with their scope and whether they declare the package directly, render dependency paths starting with the module name, show the declaring modules of outdated dependencies, and include module names in the text search. The JSON report SHALL carry the module nodes of each graph and the modules of each package location. Projects without modules SHALL render as before.

#### Scenario: Vulnerability reached from two modules
- **WHEN** a vulnerability in `lz4-java` is reached from `wr-history-api` and `wr-history-app`
- **THEN** the expanded row lists both modules with scope `runtime`, and its paths start with `wr-history-api` and `wr-history-app` shown as module names

#### Scenario: Searching by module
- **WHEN** the user types `wr-history-api` into the search box
- **THEN** the vulnerability and outdated rows whose packages that module reaches or declares stay visible

#### Scenario: Re-rendering an older result
- **WHEN** `deepward report --from` renders a JSON result written before modules existed
- **THEN** the HTML is produced without module details
