# Command-line interface

Deepward is a single binary with no runtime dependencies besides the build
tools of the projects it scans. It never prompts for input, so it runs the
same way in GitLab CI, GitHub Actions, Jenkins or a terminal.

Every command and code block below is executed by `test/docs` (doc tests).

## Commands

| Command | Purpose |
|---------|---------|
| `deepward scan [root]` | Detect projects, run analyzers, write reports |
| `deepward report --from result.json` | Render reports from a saved JSON result without rescanning |
| `deepward fix [root]` | Prepare dependency updates and fixes; `--publish` opens MRs/PRs |
| `deepward db update` | Download or refresh the offline vulnerability database |
| `deepward plugins list` | List plugins with kind, version and enabled state |
| `deepward doctor [root]` | Show where each required tool was found |
| `deepward config validate [root]` | Validate `deepward.yaml` and print the effective configuration |
| `deepward version` | Print version, commit, build date and SDK version |

Every command supports `--help`.

## Typical runs

Scan the current directory and write `deepward-out/deepward-report.html`
and `deepward-out/deepward-report.json`:

```sh
deepward scan
```

Choose formats and the output directory:

```sh
deepward scan --format html,sarif --out build/reports
```

Fail the CI job on high and critical findings:

```sh
deepward scan --fail-on high
```

Keep going when some projects cannot be built:

```sh
deepward scan --allow-partial
```

Use an SBOM built earlier instead of running the build tool, and import the
results of an external tool:

```sh
deepward scan --sbom services/api=build/bom.json --import-sarif semgrep=semgrep.sarif --allow-partial
```

Render HTML again from a saved JSON result:

```sh
deepward report --from deepward-out/deepward-report.json --format html
```

Inspect the setup:

```sh
deepward plugins list
deepward config validate
deepward version
```

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Active findings at or above `failOn` (reports are still written) |
| `2` | Usage or configuration error |
| `3` | Runtime error, including a project that failed to scan (unless `--allow-partial`) |

Without `failOn`, findings never cause a non-zero exit code. Suppressed
findings never count.

## Configuration precedence

Values are resolved in this order:

1. command-line flags;
2. `DEEPWARD_*` environment variables, named after the flag (`--fail-on` →
   `DEEPWARD_FAIL_ON`, `--format` → `DEEPWARD_FORMAT=html,json`);
3. `deepward.yaml` (or the file given by `--config`);
4. built-in defaults.

See [configuration.md](configuration.md).

## Output channels

- Reports go only to files.
- A short summary goes to stdout (`--quiet` suppresses it).
- Logs go to stderr (`--log-format json` switches them to JSON lines).
- Colors are used only when stdout is a terminal and `NO_COLOR` is not set
  (`--no-color` disables them too).

## CI detection

In **GitLab CI** (`GITLAB_CI=true`) Deepward reads `CI_PROJECT_URL`,
`CI_COMMIT_SHA`, `CI_COMMIT_REF_NAME`, `CI_DEFAULT_BRANCH`, `CI_API_V4_URL`
and `CI_PROJECT_PATH`.

In **GitHub Actions** (`GITHUB_ACTIONS=true`) it reads `GITHUB_REPOSITORY`,
`GITHUB_SHA`, `GITHUB_REF_NAME` / `GITHUB_HEAD_REF`, `GITHUB_SERVER_URL`,
`GITHUB_API_URL` and the default branch from `GITHUB_EVENT_PATH`.

Elsewhere (Jenkins, TeamCity, …) pass the same values as flags:

```sh
deepward scan --repo-url https://git.example.com/team/app --commit 0a1b2c3 --branch feature/x --target-branch main
```
