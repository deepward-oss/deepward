# Spec Delta

## ADDED Requirements

### Requirement: NuGet pins in the entering modules
In a NuGet project with modules, a pin of a transitive package SHALL be a `PackageVersion` in `Directory.Packages.props` when Central Package Management pins transitively (`CentralPackageTransitivePinningEnabled`). Otherwise it SHALL add a `PackageReference` to each module through which the package enters the project from outside, that is, each module that reaches it without passing through another module.

#### Scenario: Pin in the module that brings the package
- **WHEN** `Web` and `Api` reference `Core`, `Core` brings vulnerable `System.Text.Json` through a package, and transitive pinning is off
- **THEN** the change request adds a `PackageReference` for `System.Text.Json` only to `Core.csproj`

#### Scenario: Central transitive pinning
- **WHEN** `Directory.Packages.props` sets `CentralPackageTransitivePinningEnabled` to `true`
- **THEN** the change request adds only a `PackageVersion` to `Directory.Packages.props`

#### Scenario: One fix for the whole solution
- **WHEN** a vulnerable package is reached by thirty project files of one CPM project
- **THEN** one change request fixes it, editing `Directory.Packages.props` once

### Requirement: Requests of merged projects are closed
When an open bot request belongs to a project that is no longer detected and whose directory now lies inside a scanned project of the same ecosystem, the system SHALL close it with a comment naming the project that replaces it. Requests of projects that are missing for other reasons SHALL stay open.

#### Scenario: Project file became a module
- **WHEN** a request for project `nuget:src/Web` is open and `src/Web/Web.csproj` is now a module of `nuget:.`
- **THEN** the request is closed with a comment saying its project is now part of `nuget:.`

#### Scenario: Project removed from the repository
- **WHEN** a request belongs to project `nuget:legacy` and no scanned project contains `legacy/`
- **THEN** the request stays open
