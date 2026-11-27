# Fixes and merge/pull requests

`deepward fix` turns remediations into verified edits:

1. **Scan** as usual.
2. **Group** the remediations: by the `fix.groups` rules (purl patterns),
   then by `fix.grouping` (see [configuration.md](configuration.md)). The
   default is one request per package: `security/<package>` for
   vulnerabilities, `updates/<package>` for outdated dependencies. When a
   package has both, the update is merged into the security fix: Deepward
   first tries the newer update version, and the request then fixes the
   vulnerabilities and updates the package in one change. If that version
   breaks the build or is still vulnerable, the security fix goes alone and
   the update is reported as `verification-failed`.
3. **Edit** a private copy of the project. Only the version token changes.
   For Maven that is the literal or the `${property}`, for sbt the literal or
   the shared `val`, for NuGet the `PackageReference`, `$(Property)` or
   `Directory.Packages.props`. Formatting and comments stay untouched. For
   npm, Yarn, pnpm, Go and Cargo the package manager then updates the
   lockfile (see the [ecosystem docs](ecosystems)).
4. **Verify** by rebuilding the graph on the edited copy and running the same
   analyzers. A change is kept only if the targeted findings are gone and the
   build still works. By default (`fix.verify: batch`) independent changes of
   different requests are verified together in one rebuild, and a batch that
   breaks the build is split until the breaking change is found;
   `fix.verify: each` rebuilds for every change (see
   [configuration.md](configuration.md#speed)). For a vulnerable transitive
   dependency there are two ways: pin the transitive version (Maven
   `dependencyManagement`, NuGet CPM or `PackageReference`, npm `overrides`, pnpm `pnpm.overrides`, Yarn
   `resolutions`, `go get`, `cargo update --precise`), or upgrade the direct
   dependency that brings it in to a version that brings a fixed one.
   `fix.transitive` decides which comes first: `upgrade` (the default) or
   `pin`; the other is tried when the first does not pass verification.
   For sbt Deepward prints a ready-to-paste `dependencyOverrides` line
   instead of a pin.
5. **Assemble** every change request on a clean copy that holds only its
   verified changes. The `fix.postTasks` commands run there once, for example
   to regenerate code or checksums that depend on versions; their output
   joins the request's commit. A failing task drops the request.
6. **Report**: without `--publish`, Deepward prints a unified diff per change
   request and writes nothing. With `--publish`, it creates or updates merge
   or pull requests.

Changes that cannot be made automatically are listed as `manual`, with the
reason and, where possible, a suggestion. Edits that do not pass
verification are listed as `verification-failed` in the summary and logged
as warnings (`change rejected`), with the reason.

### Unverified changes

"Verification failed" means Deepward still sees the advisory after the
change, not that the change is useless: a `dependencyManagement` pin may not
reach every path, the advisory may be about another artifact or a wider
version range than what is on the classpath, or another dependency may bring
the package again. With

```yaml
fix:
  unverified:
    publish: true
```

Deepward opens a request for such changes too, as long as the project still
builds with them. It takes the first such alternative in the order of
preference (`fix.transitive`), titles the request
`Deepward: [unverified] …`, adds the `deepward-unverified` label
(`fix.unverified.labels`), and starts the description with a warning, the
reason for every unverified change and a command that shows where the
package comes from (for Maven `mvn dependency:tree -Dverbose -Dincludes=…`).
These requests are candidates for a person to review: do not merge them
automatically. When a later run verifies the change, the same request is
updated and loses the marking. Changes that break the build or whose post
tasks fail are never published.

## Behavior on the platform

- **One request per group**, on a deterministic branch from `fix.branch`,
  by default `deepward/<ecosystem>/<project directory>/<group>`, for example
  `deepward/maven/security-log4j-core` for a root project and
  `deepward/npm/web/admin/security-axios` for `web/admin`.
- **Titles**: `Deepward: fix vulnerabilities: axios 1.13.4 → 1.20.0` or
  `Deepward: update lodash 4.17.20 → 4.17.21` for one package;
  `Deepward: fix vulnerabilities in 3 packages, update 5 dependencies` for a
  `type` or `project` batch; `Deepward: update jackson (3 changes)` for a
  `groups` rule. ` in <directory>` is added for projects outside the root,
  ` (not built)` as described below. `fix.title` replaces these titles with your own
  patterns per kind of request, for example Conventional Commits (see
  [configuration.md](configuration.md#titles)); the commit subject then
  follows the title.
- **Re-runs update** the existing request: the branch is recreated from the
  current target branch (force-push) and the description is refreshed. No
  duplicates are created.
- **Moved changes**: when a change moves to another request (the grouping
  changed, or an update was merged into a security fix), the old request is
  closed with a comment naming the request that now contains it.
- **Commits by people pause** a request: Deepward stops force-pushing and
  comments once.
- **Stale requests are closed** with a comment when the update is no longer
  needed, for example because the package was updated by hand.
- **Merged projects**: when a project is no longer detected because it became
  part of a larger one (a .NET project file that is now a module of its
  solution), its open requests are closed with a comment naming the new
  project. Requests of projects that failed, were excluded or were removed
  stay open.
- **Limit**: at most `fix.limit` (default 5) open bot requests. Fixes for
  critical and high vulnerabilities bypass the limit. Other changes are
  reported as deferred.
- **Not built**: fixes whose findings are all in packages that are not
  compiled into the project (see [Go modules](ecosystems/go.md)) are titled
  "(not built)". Their description explains that the vulnerable code does
  not ship. They come after all other requests and never bypass the limit,
  whatever their severity.
- **Labels**: `fix.labels` (default `dependencies`), plus
  `fix.securityLabels` (default `security`) for vulnerability fixes, plus
  `fix.unverified.labels` (default `deepward-unverified`) for unverified
  changes.
- The description has a change table, the fixed advisories with links and
  severity, release notes when known, and a hidden metadata marker.

Commits are created through the platform API (the GitLab Commits API, the
GitHub Git Data API), so CI does not need git credentials. The commit author
is `Deepward` (`deepward-bot@users.noreply.deepward.dev`). GitHub shows API-created commits as unverified. If your
target branch requires signed commits, use a GitHub App with commit signing
(planned).

## Tokens

| Platform | Variable | Scopes |
|----------|----------|--------|
| GitLab (gitlab.com, self-managed) | `DEEPWARD_GITLAB_TOKEN` | project or group access token with `api` scope, role Developer or higher |
| GitHub (github.com, Enterprise Server) | `DEEPWARD_GITHUB_TOKEN` | `contents: write`, `pull-requests: write`, `issues: write` (labels and comments) |

Tokens are never passed to build tools and are masked in all output.
Without a token, `--publish` stops with exit code 2 before contacting
anything.

The platform and repository come from CI variables. Elsewhere, use
`--platform`, `--repo`, `--api-url` and `--repo-url`. For self-managed
instances, the API URL is derived from the repository URL
(`https://git.acme.local/api/v4`, `https://ghe.acme.local/api/v3`).

## Recommended CI setup

Run the scan in every pipeline with a read-only job. Run `fix --publish` in a
separate scheduled job that has the write token. The scan job then cannot
write to the repository even though it builds untrusted dependency code.

### GitLab CI

```yml
deepward-scan:
  stage: test
  image: maven:3.9-eclipse-temurin-17
  script:
    - deepward scan --fail-on high
  artifacts:
    when: always
    paths:
      - deepward-out/
  cache:
    paths:
      - .m2/
  variables:
    MAVEN_OPTS: "-Dmaven.repo.local=.m2"

deepward-fix:
  stage: deploy
  image: maven:3.9-eclipse-temurin-17
  rules:
    - if: $CI_PIPELINE_SOURCE == "schedule"
  script:
    - deepward fix --publish
  variables:
    DEEPWARD_GITLAB_TOKEN: $DEEPWARD_BOT_TOKEN
```

### GitHub Actions

```yml
name: deepward
on:
  pull_request:
  schedule:
    - cron: "0 6 * * 1"
permissions:
  contents: read
jobs:
  scan:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-java@v4
        with:
          distribution: temurin
          java-version: "17"
      - run: deepward scan --format html,json,sarif --fail-on high
      - uses: github/codeql-action/upload-sarif@v3
        if: always()
        with:
          sarif_file: deepward-out/deepward-report.sarif
  fix:
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    permissions:
      contents: write
      pull-requests: write
      issues: write
    steps:
      - uses: actions/checkout@v4
      - run: deepward fix --publish
        env:
          DEEPWARD_GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```
