# Spec Delta

## ADDED Requirements

### Requirement: NuGet project boundaries
A NuGet project SHALL contain the project files (`*.csproj`, `*.fsproj`, `*.vbproj`) under the same boundary, chosen per project file: the directory of the nearest `Directory.Packages.props` at or above it; else the directory of the nearest `.sln` or `.slnx` at or above it that lists it (the first by path when two are equally near); else its own directory. Every project file SHALL belong to exactly one project.

#### Scenario: Central Package Management root
- **WHEN** the repository root has `Directory.Packages.props` and project files `src/Web/Web.csproj`, `src/Core/Core.csproj` and `tests/Core.Tests/Core.Tests.csproj`
- **THEN** one project `nuget:.` is reported with the three project files as its modules

#### Scenario: Solution without CPM
- **WHEN** `App.sln` at the root lists `Web/Web.csproj` and `Core/Core.csproj`, and there is no `Directory.Packages.props`
- **THEN** one project `nuget:.` is reported with both project files

#### Scenario: Project file in no solution
- **WHEN** `legacy/Old.csproj` is listed by no solution and has no `Directory.Packages.props` above it
- **THEN** it forms the project `nuget:legacy` as before

#### Scenario: Project file in two solutions
- **WHEN** `Core/Core.csproj` is listed by `App.sln` and `tools/Tools.sln`, and there is no `Directory.Packages.props`
- **THEN** it belongs only to the project of `App.sln`, the nearer solution above it

### Requirement: NuGet project files as modules
In a NuGet project with more than one project file, every project file SHALL be a module node identified by its path and named by its file name without extension, with edges to the packages it references and to the project files it references through `ProjectReference`. Each package SHALL record every module whose resolved assets contain it.

#### Scenario: Package flowing through a project reference
- **WHEN** `Web.csproj` references `Core.csproj`, which references `Newtonsoft.Json` 12.0.1
- **THEN** the graph has an edge from `Web` to `Core` and from `Core` to `Newtonsoft.Json@12.0.1`, and both modules reach `Newtonsoft.Json`

#### Scenario: One project file
- **WHEN** a project has a single project file
- **THEN** its graph has no module nodes, as before

### Requirement: One restore per NuGet project
The system SHALL run `dotnet restore` once for a project that has a solution, on that solution; otherwise once per project file that no other project file of the project references. It SHALL then read the assets file of every project file of the project; a project file left without an assets file (a solution restore skips projects its active configuration does not build) SHALL be restored on its own once, and its assets file read again.

#### Scenario: Solution with thirty projects
- **WHEN** a project is bounded by `App.sln`, which lists thirty project files
- **THEN** `dotnet restore App.sln` runs once, and the graph covers the thirty project files

#### Scenario: CPM root without a solution
- **WHEN** a CPM project has `Web.csproj` (referencing `Core.csproj`) and `Worker.csproj`, and no solution
- **THEN** `dotnet restore` runs for `Web.csproj` and `Worker.csproj` but not for `Core.csproj`

#### Scenario: Project file skipped by the solution restore
- **WHEN** `App.sln` lists `Web.csproj` and `Tools.csproj`, and its active configuration builds only `Web.csproj`
- **THEN** `dotnet restore App.sln` runs, then `dotnet restore Tools.csproj`, and the graph covers both project files
