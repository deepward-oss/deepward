# Configuration

Deepward reads `deepward.yaml` (or `deepward.yml`) from the scan root, or the
file given by `--config`. Every key is optional. Unknown keys and invalid
values are errors that name the key and line, for example:

```text
deepward.yaml:3: failon: unknown key "failon" (did you mean "failOn"?)
```

`deepward config validate` checks the file and prints the effective
configuration with secrets masked. A JSON Schema for editors is published at
[`schema/deepward.schema.json`](../schema/deepward.schema.json).

Every YAML block in this file is validated by `test/docs`.

## Quality gate and suppressions

```yaml
# Exit with code 1 when active findings of this severity or higher exist.
failOn: high

ignore:
  - id: CVE-2022-1471                 # rule ID or any alias (CVE, GHSA, OSV)
    purl: pkg:maven/org.yaml/*        # optional: limit to matching packages
    reason: SnakeYAML Constructor is not used
    expires: 2027-01-01               # optional: the rule stops applying after this date
```

Every ignore rule needs a reason. Expired rules stop applying and produce a
warning. Suppressed findings are listed in their own report section.

## Scan scope

```yaml
exclude:
  - examples/
  - legacy/*
allowPartial: false
concurrency: 4
pluginTimeout: 30m
```

`target`, `bin`, `obj`, `node_modules` and `.git` are always skipped.

## Reports and cache

```yaml
output:
  dir: deepward-out
  formats: [html, json, sarif]
cache:
  dir: .deepward-cache
  ttl: 6h
offline: false
```

## Plugins

Each plugin has a section under `plugins.<id>`. `enabled: false` turns a plugin
off. The other keys are documented with the plugin.

```yaml
plugins:
  outdated-dependencies:
    enabled: false
```

The `dependency-vulnerabilities` analyzer can drop scopes, and it decides
what happens to packages that are resolved but not compiled (scope
`not-built`, see [Go modules](ecosystems/go.md)). By default, advisories on
them are reported as findings **marked "not built"**, with their real
severity and fixes. Such findings:

- do not count toward `failOn`;
- are listed in a separate "Not built" section of the HTML report and on a
  separate line of the CLI summary;
- carry `notBuilt: true` in JSON and the `not-built` tag in SARIF;
- get change requests from `deepward fix` without priority (see
  [publishing.md](publishing.md)).

`notBuilt: warn` lists them in one `not-built-skipped` warning per project
instead:

```yaml
plugins:
  dependency-vulnerabilities:
    excludeScopes: [test, development]
    notBuilt: report            # or warn
    maxPaths: 5                 # dependency paths shown per finding
```

## Tools, add-ons and setup steps

Tools are resolved by the toolchain. Add-ons extend the build tools. Setup
steps run before graphs are built. See [toolchain.md](toolchain.md) and
[integrations.md](integrations.md).

```yaml
tools:
  mvn:
    args: ["-s", "ci/settings.xml"]
    env:
      MAVEN_OPTS: "-Xmx2g"
      NEXUS_TOKEN: ${env:NEXUS_TOKEN}
projects:
  services/api:
    setup:
      - tool: mvn
        args: [-q, install, -DskipTests]
        dir: ../shared
```

## Fixes and change requests

```yaml
fix:
  branch: deepward/{ecosystem}/{project}/{group}
  grouping: package             # package, type or project
  limit: 5
  labels: [dependencies]
  securityLabels: [security]
  maxCandidates: 5
  minSeverity: high             # fix only findings at or above this severity
  kinds: [vulnerability]        # fix only these finding kinds (default: all)
  concurrency: 2                # verification jobs run in parallel
  verify: batch                 # batch or each, see Speed
  transitive: upgrade           # upgrade or pin: what is tried first for transitive vulnerabilities
  title:                        # title patterns per kind of request, see Titles
    security: "fix(deps): bump {name} from {from} to {to}"
  groups:
    - name: jackson
      match: ["pkg:maven/com.fasterxml.jackson*/*"]
platform:
  type: gitlab
  apiUrl: https://git.acme.local/api/v4
  repo: team/billing
```

### Which findings get change requests

By default every finding with a fix gets a change request: vulnerabilities
of any severity and updates of outdated dependencies. Two keys narrow this:

- `minSeverity` keeps findings at or above a severity (`critical`, `high`,
  `medium`, `low`). Updates of outdated dependencies have no severity, so any
  value leaves them out. `deepward fix --min-severity critical` overrides it
  for one run.
- `kinds` keeps findings of the listed kinds: `vulnerability`, `outdated`,
  `code-issue`, ...

Only critical vulnerabilities:

```yaml
fix:
  minSeverity: critical
```

Only updates, no security fixes:

```yaml
fix:
  kinds: [outdated]
```

Open requests of the findings that are left out are neither updated nor
closed: they stay as they are until you close them or widen the filters.

### Titles

`title` sets the title of change requests with one pattern per kind of
request, using `{placeholder}`s like `branch`. A kind without a pattern keeps
the default title, for example
`Deepward: fix vulnerabilities: at.yawk.lz4:lz4-java 1.11.2 → 1.11.4`.

| Kind | Request |
|------|---------|
| `security` | fixes vulnerabilities with one package change (an update merged into the fix included) |
| `pin` | pins a transitive version; uses `security` when not set |
| `update` | updates one outdated dependency |
| `group` | has several package changes (`groups` rules, `grouping: type` or `project`) |

| Placeholder | Value |
|-------------|-------|
| `{package}` | the package as its ecosystem names it: `org.apache.kafka:kafka-clients`, `@scope/name` |
| `{name}` | the part after the last `:` or `/`: `kafka-clients` |
| `{from}`, `{to}` | the versions |
| `{update}` | `patch`, `minor` or `major` (updates only) |
| `{severity}` | the highest severity of the fixed vulnerabilities |
| `{count}` | the number of package changes |
| `{group}` | the group: `kafka-clients`, `jackson`, `security`, `all` |
| `{project}` | the project directory; empty at the repository root |
| `{ecosystem}` | `maven`, `npm`, `go`, ... |

In a `group` request `{package}`, `{name}`, `{from}`, `{to}` and `{update}`
are empty. At the repository root `{project}` disappears together with the
brackets around it and a preposition before it (`in`, `for`, `at`), so
`fix({project}): …` becomes `fix: …`. Requests with unverified changes
always start with `[unverified] `, and requests for packages that are not
built always end with ` (not built)`. When a title
comes from a pattern, the commit subject is the same title, so Conventional
Commits reach the git history too. Titles are cut to 255 characters.

Conventional Commits:

```yaml
fix:
  title:
    security: "fix(deps): bump {name} from {from} to {to}"
    pin: "fix(deps): pin {name} to {to}"
    update: "chore(deps): bump {name} from {from} to {to}"
    group: "chore(deps): update {group} ({count} packages)"
```

```text
fix(deps): bump kafka-clients from 4.4.0 to 4.5.0
fix(deps): pin zookeeper to 3.9.5
chore(deps): bump grpc-stub from 1.85.1 to 1.86.0
chore(deps): update jackson (3 packages)
```

The project as the scope, for monorepos (`fix: …` at the root):

```yaml
fix:
  title:
    security: "fix({project}): {name} {from} → {to}"
    update: "chore({project}): {name} {from} → {to}"
```

The project after a preposition (`… in services/api`, nothing at the root):

```yaml
fix:
  title:
    security: "Fix {name} {from} → {to} in {project}"
    pin: "Pin {name} to {to} in {project}"
    update: "Update {name} {from} → {to} ({update}) in {project}"
    group: "Update the {group} dependencies ({count}) in {project}"
```

Severity or update type first, to scan the request list:

```yaml
fix:
  title:
    security: "[{severity}] {package}: {from} → {to}"
    update: "[{update}] {package}: {from} → {to}"
```

Like Dependabot:

```yaml
fix:
  title:
    security: "Bump {package} from {from} to {to}"
    update: "Bump {package} from {from} to {to}"
    group: "Bump the {group} group with {count} updates"
```

Changing a pattern renames open requests on the next run; their branches,
and so the requests themselves, stay the same.

### Transitive vulnerabilities

A vulnerable transitive dependency can be fixed by pinning its version or by
upgrading the direct dependency that brings it in. `transitive` decides
which Deepward tries first:

- `upgrade` (default): upgrade the direct dependency to the oldest release
  that brings a fixed version. No pin to maintain, but a bigger change.
- `pin`: pin the fixed version (Maven `dependencyManagement` in the root
  POM, npm `overrides`, ...). One small change, independent of whether the
  direct dependency has a release with the fix.

The other one is tried when the first does not pass verification.

### Post tasks

Like Renovate's `postUpgradeTasks`, `postTasks` run once per change
request: after its changes are verified, in a copy of the project that holds
only that request's changes, and before they are committed. What they
regenerate (generated code, checksums, lockfiles of other tools) goes into
the same commit as the version edits.

```yaml
fix:
  postTasks:
    - tool: mvn                       # resolved like setup steps
      args: [-q, generate-sources]
      dir: .                          # relative to the project directory
      env: {MAVEN_OPTS: -Xmx1g}
      files: ["**/src/main/generated/**", "**/*.sha256"]
      ecosystems: [maven]             # optional: only these ecosystems
      projects: [services/api]        # optional: only these project directories
```

- `files` are globs relative to the repository root (`**` matches any
  number of directories). Changes to other files, including deletions, are
  undone; without `files` every change outside excluded directories such as
  `target/` is kept.
- A task that fails drops the change request, which is reported as failed
  with the command and the last line of its output as the reason; no other
  alternative is tried for it. Other change requests are not affected.
- The tasks do not run before verification, so verification does not see
  their output. Lockfiles of npm, pnpm, Yarn and Cargo are regenerated by
  Deepward itself when it edits the manifests, so they are verified.
- Their commands are listed in the change request.
- The commands come from `deepward.yaml` in the repository and run with the
  rights of the CI job, the same as setup steps.

### Unverified changes

```yaml
fix:
  unverified:
    publish: true                     # default false
    labels: [deepward-unverified]
```

By default a change after which the targeted findings are still reported
gets no request and is listed as `verification-failed`. With `publish`, it
gets a request marked `[unverified]` with a warning and the reason; see
[publishing.md](publishing.md#unverified-changes).

### Speed

Every change is verified by building the dependency graph again in a copy of
the project. `verify` decides how many builds that takes:

- `batch` (default): changes are verified in rounds. Each round takes the
  next untried alternative of every vulnerability or update still without a
  verified change, groups the changes of different change requests that
  affect different packages into batches and builds the graph once per
  batch. A batch that no longer builds is split in halves until the change
  that breaks it is found; changes that break the build only together are
  verified one by one. Twelve independent change requests take one build
  instead of twelve.
- `each`: every alternative gets its own build, about one per change
  request plus one per rejected alternative. Use it when every change
  request must be verified exactly as it will be merged, on its own.

Independence is judged from the current dependency graph, so a new version
that brings packages it did not bring before can still interact with
another change in its batch. It can make a fix look ineffective: when
upgrading `commons-compress` from 1.20 to 1.26 newly brings a vulnerable
`commons-lang3`, the `commons-text` upgrade that fixes `commons-lang3` in the
same batch still sees it reported, so Deepward falls back to the next
alternative (a pin). Less often it can make a change look like a fix only
thanks to the other change. Every published change is still verified, but
the choice between alternatives can differ from `each`.

Changes are independent when the packages they may alter do not overlap:
for a pin or an upgrade, the package and every package it currently brings
in. A change that does not say which packages it alters (text edits,
external analyzers) is verified in a batch of its own. Both modes produce the
same change requests for independent changes. The `fixes verified` log line
reports `graphBuilds`, the number of builds verification needed.

`concurrency` (default 2) runs that many verification jobs at a time (batches
with `batch`, change requests with `each`), each with its own copy of the
project and its own build process; raise it on machines with spare CPU and
memory. Narrowing the findings with `minSeverity` or `kinds` cuts the number
of builds too.

## Logging

```yaml
log:
  level: info        # debug, info, warn or error
  format: text       # text or json
  progress: true     # log what scan and fix are doing
```

Logs go to stderr. With `progress` (the default), `deepward scan` and
`deepward fix` log the projects they found, each dependency graph and
analyzer with its duration, and every change `deepward fix` verifies with its
result, so a long run shows where the time goes:

```text
level=INFO msg="verifying fix group" group=security/at.yawk.lz4:lz4-java project=maven:. progress=1/3
level=INFO msg="verifying change" project=maven:. change="upgrade org.apache.kafka:kafka-clients 4.4.0 → 4.5.0 to bring at.yawk.lz4:lz4-java 1.11.4"
level=INFO msg="change verified" project=maven:. change="upgrade org.apache.kafka:kafka-clients 4.4.0 → 4.5.0 to bring at.yawk.lz4:lz4-java 1.11.4" duration=41.2s
```

`--log-format` overrides `format`, and `--quiet` raises the level to `warn`,
which hides progress.

`grouping` decides how many change requests are opened:

| Value | Change requests |
|-------|-----------------|
| `package` (default) | one per package |
| `type` | per project, one for security fixes and one for other updates |
| `project` | one per project with every fix |

`groups` rules are applied first: a package that matches a rule always goes
to that rule's request. See [publishing.md](publishing.md) for titles,
branches and what happens to open requests when the grouping changes.

`branch` is the branch name template:

| Placeholder | Value |
|-------------|-------|
| `{ecosystem}` | `maven`, `npm`, `go`, ... |
| `{project}` | the project directory, for example `services/api`; empty for a project at the repository root |
| `{group}` | the group: `security-axios`, `updates-lodash`, `security`, `all` or a rule name |

`{group}` is required, and the template must start with fixed text such as
`deepward/`: Deepward finds its own requests by that prefix. An empty
placeholder drops the `/` next to it, so a root project gets
`deepward/npm/security-axios`. If two requests would get the same branch,
`deepward fix` stops and asks you to add `{project}` or `{ecosystem}`.
