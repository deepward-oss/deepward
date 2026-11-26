# cli Specification

## Purpose
Define the command-line interface of `deepward`: commands, configuration sources and precedence, output channels, exit codes and behavior inside CI systems.

## Requirements

### Requirement: Command set
The system SHALL provide the commands `scan` (build graphs, analyze vulnerabilities and updates, write reports), `report` (render reports from a saved JSON result), `fix` (prepare edits, optionally publish MRs/PRs), `db update` (download or refresh the offline vulnerability database), `plugins list`, `doctor` (toolchain diagnostics), `config validate` and `version`. Every command SHALL support `--help`.

#### Scenario: Default scan
- **WHEN** `deepward scan` runs in a repository root with no flags
- **THEN** the current directory is scanned and `deepward-report.html` and `deepward-report.json` are written to `./deepward-out/`

#### Scenario: Selecting formats and output directory
- **WHEN** `deepward scan --format html,sarif --out build/reports` runs
- **THEN** only the HTML and SARIF reports are written to `build/reports`

#### Scenario: Unknown command
- **WHEN** `deepward scna` runs
- **THEN** the command exits with the usage error code and suggests `scan`

### Requirement: Configuration file and precedence
The system SHALL read `deepward.yaml` (or a path given by `--config`) from the scan root. Values are resolved with precedence: CLI flags, then `DEEPWARD_*` environment variables, then the configuration file, then built-in defaults. An unknown key or invalid value MUST fail the run with a message naming the key and file location.

#### Scenario: Flag overrides file
- **WHEN** `deepward.yaml` sets `failOn: medium` and the run uses `--fail-on critical`
- **THEN** the effective threshold is `critical`

#### Scenario: Typo in configuration
- **WHEN** `deepward.yaml` contains the unknown key `failon`
- **THEN** the run exits with the configuration error code and reports `unknown key "failon"` with the line number

#### Scenario: Validating configuration
- **WHEN** `deepward config validate` runs on a valid file
- **THEN** it exits with code 0 and prints the effective configuration with secrets masked

### Requirement: Exit codes
The system SHALL use stable exit codes: `0` success; `1` active findings at or above the `failOn` threshold; `2` usage or configuration error; `3` runtime error, including any project that failed to scan unless `--allow-partial` is set. Without a `failOn` setting, findings SHALL NOT cause a non-zero exit.

#### Scenario: Quality gate triggered
- **WHEN** `failOn: high` is set and one active high finding exists
- **THEN** the exit code is 1 and reports are still written

#### Scenario: Suppressed findings do not fail the gate
- **WHEN** the only high finding is suppressed by an active ignore rule
- **THEN** the exit code is 0

#### Scenario: Partial scan failure
- **WHEN** one of three projects fails to build its graph and `--allow-partial` is not set
- **THEN** reports for all projects are written and the exit code is 3

### Requirement: Output channels
The system SHALL write reports only to files, write a human-readable summary to stdout and logs to stderr. `--log-format json` SHALL switch logs to JSON lines and `--quiet` SHALL suppress the summary. Colors SHALL be used only when the output is a terminal and `NO_COLOR` is not set.

#### Scenario: Running in a CI log
- **WHEN** stdout is not a terminal
- **THEN** the summary contains no ANSI escape sequences

### Requirement: Progress logging
The system SHALL log progress on stderr at info level unless `log.progress` is false: the number of detected projects, each fact (such as a dependency graph) and analyzer run with its status and duration, the end of the scan, and for `deepward fix` every change group and every change being verified with its result and duration. Verification scans SHALL not log scan progress of their own. The `log` section SHALL set `level` (debug, info, warn, error) and `format` (text, json); `--log-format` overrides `format` and `--quiet` raises the level to warn.

#### Scenario: Long fix run
- **WHEN** `deepward fix` verifies an ancestor upgrade that takes 40 seconds to rebuild
- **THEN** stderr shows "verifying change" with the change before the rebuild and "change verified" or "change rejected" with the duration after it

#### Scenario: Progress turned off
- **WHEN** `log.progress` is false
- **THEN** stderr shows no progress lines, only warnings and errors

### Requirement: Non-interactive CI operation
The system MUST NOT prompt for input. It SHALL detect GitLab CI and GitHub Actions from their standard environment variables to fill in repository URL, commit, branch and default target branch, and SHALL accept the same values as flags for any other CI.

#### Scenario: GitLab CI pipeline
- **WHEN** the run happens in a GitLab CI job with `CI_PROJECT_URL`, `CI_COMMIT_SHA` and `CI_DEFAULT_BRANCH` set
- **THEN** the report metadata contains these values and `fix --publish` targets `CI_DEFAULT_BRANCH` without extra flags

#### Scenario: Generic CI
- **WHEN** the run happens in Jenkins with `--repo-url`, `--commit` and `--target-branch` given
- **THEN** behavior is identical to an auto-detected CI

### Requirement: Distribution
The system SHALL ship as a single static binary for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64, and as a container image that contains only the binary. The `version` command SHALL print the version, commit and build date.

#### Scenario: Running without a runtime
- **WHEN** the linux/amd64 binary is copied into a minimal image that has a build tool but no Go or Node.js
- **THEN** `deepward version` and `deepward scan` work
