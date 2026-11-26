# Add-ons, setup steps and integrations

Three extension points need no Go code at all.

## Build-tool add-ons

Ecosystem plugins inject build-tool plugins and helper tools without editing
your build files. Each add-on has a name and a default version, and
configuration can override it:

```yaml
ecosystems:
  maven:
    addons:
      dependency-plugin:
        version: 3.6.1            # the corporate mirror lacks the default 3.8.1
  sbt:
    addons:
      corp-resolvers:
        kind: sbt-plugin-file     # injected with --addPluginSbtFile
        file: ci/corp-resolvers.sbt
```

| Kind | Used by | Effect |
|------|---------|--------|
| `maven-goal` | maven | Runs `coordinates:version:goal` by full coordinates |
| `sbt-plugin-file` | sbt | Passed to sbt as `--addPluginSbtFile=<file>` |
| `dotnet-tool` | nuget | Restored from the tool manifest and run with `dotnet tool run` |

`deepward plugins list` shows the plugins. Each ecosystem's documentation
lists its built-in add-ons.

## Setup steps

Setup steps run before dependency graphs are built: for every project of an
ecosystem, or for one project (keyed by its directory). They use the same
tool resolution and settings as everything else. A failed step fails only the
projects it applies to.

```yaml
ecosystems:
  nuget:
    setup:
      - tool: dotnet
        args: [tool, restore]
projects:
  services/api:
    setup:
      - tool: mvn
        args: [-q, install, -DskipTests]
        dir: ../shared-model
```

## Integrations

An integration runs any CLI and parses its output with a registered output
format. Deepward then treats it as a normal plugin, with an ID, `enabled`, a
timeout and isolation, and its results appear in reports, the quality gate
and ignore rules.

| Key | Meaning |
|-----|---------|
| `type` | `analyzer` (default) or `fact-producer` |
| `tool` | Logical tool name, resolved by the toolchain (see [toolchain.md](toolchain.md)) |
| `constraint` | Optional version constraint |
| `args` | Arguments. Templates: `{{.OutputFile}}`, `{{.OutputDir}}`, `{{.ProjectDir}}`, `{{.ProjectRel}}`, `{{.ProjectID}}`, `{{.Ecosystem}}`, `{{.RepoRoot}}` |
| `output` | Output format: `sarif` (findings) or `cyclonedx` (dependency graph) |
| `fact` | Fact produced by a `fact-producer` (e.g. `dependency-graph`) |
| `stdout` | Parse standard output instead of `{{.OutputFile}}` |
| `outputPath` | File to parse (template, default `{{.OutputFile}}`), for tools that write into `{{.OutputDir}}` |
| `exitCodes` | Exit codes that count as success (default `[0]`) |
| `scope` | `project` (default) or `workspace` for analyzers |
| `ecosystems` | Only run for projects of these ecosystems |
| `kind` | Override the finding kind |
| `env`, `timeout`, `description` | As usual |

### Semgrep as an analyzer

```yaml
integrations:
  semgrep:
    tool: semgrep
    args: [scan, --config, auto, --sarif, --output, "{{.OutputFile}}", "{{.ProjectDir}}"]
    output: sarif
    exitCodes: [0, 1]
    env:
      SEMGREP_SEND_METRICS: "off"
```

### cyclonedx-maven-plugin as the dependency-graph producer

```yaml
integrations:
  cyclonedx-maven:
    type: fact-producer
    fact: dependency-graph
    ecosystems: [maven]
    tool: mvn
    args:
      - -q
      - org.cyclonedx:cyclonedx-maven-plugin:2.9.1:makeAggregateBom
      - -DoutputFormat=json
      - -DoutputName=bom
      - -DoutputDirectory={{.OutputDir}}
      - -Dcyclonedx.skipAttach=true
    output: cyclonedx
    outputPath: "{{.OutputDir}}/bom.json"
```

### CycloneDX for .NET as the dependency-graph producer

```yaml
ecosystems:
  nuget:
    setup:
      - tool: dotnet
        args: [tool, restore]
integrations:
  cyclonedx-dotnet:
    type: fact-producer
    fact: dependency-graph
    ecosystems: [nuget]
    tool: dotnet
    args: [tool, run, dotnet-CycloneDX, "{{.ProjectDir}}", --json, --output, "{{.OutputDir}}", --filename, bom.json]
    output: cyclonedx
    outputPath: "{{.OutputDir}}/bom.json"
```

If an integration names an output format that is not registered, Deepward
stops with a configuration error that names the integration and the format.
