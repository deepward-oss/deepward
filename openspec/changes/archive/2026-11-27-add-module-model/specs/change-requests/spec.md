# Spec Delta

## ADDED Requirements

### Requirement: Affected modules in the change request
When the findings a change request fixes come from a project with modules, its description SHALL list, for each package change, the modules those findings affect. Change requests of projects without modules SHALL keep their current description.

#### Scenario: Pin affecting two modules
- **WHEN** a change request pins `lz4-java` to 1.11.4 and the fixed findings list modules `wr-history-api` and `wr-history-app`
- **THEN** the description names `wr-history-api` and `wr-history-app` for that change
