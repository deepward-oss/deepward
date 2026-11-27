# Spec Delta

## ADDED Requirements

### Requirement: Modules of an outdated dependency
In a project with modules, an outdated-dependency finding SHALL stay one per package version in the project, list the modules that declare the dependency with their scopes, and point at the declaring line in the manifest of the first of those modules by name.

#### Scenario: Library declared in six modules
- **WHEN** `jackson-databind` 2.22.4 is declared by six modules and 2.23.0 is available
- **THEN** there is one outdated finding listing the six modules, located in the manifest of the first of them by name
