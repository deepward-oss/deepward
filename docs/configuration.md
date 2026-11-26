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
  groups:
    - name: jackson
      match: ["pkg:maven/com.fasterxml.jackson*/*"]
platform:
  type: gitlab
  apiUrl: https://git.acme.local/api/v4
  repo: team/billing
```

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
