# Writing Deepward plugins

Deepward is a small engine plus plugins. The engine (`internal/engine`) knows
only the contracts in [`pkg/sdk`](../pkg/sdk). Every ecosystem, analyzer,
reporter, hosting platform, tool resolver and output format, including the
built-in ones, is a plugin.

> The SDK is **v0**: contracts may still change. New capabilities are added as
> optional interfaces rather than by changing existing methods. `deepward
> plugins list` prints the SDK version.

## Plugin kinds

| Kind | Contract | Orchestrated by | Built-in examples |
|------|----------|-----------------|-------------------|
| `ecosystem` | `sdk.Ecosystem` (+ optional `FactProducer`, `RemediationRealizer`, `AddonProvider`, `ToolDefiner`, `deps.VersionRegistry`, `deps.DependencyLister`) | engine | `maven`, `sbt`, `nuget`, `npm`, `go`, `cargo` |
| `analyzer` | `sdk.Analyzer` | engine | `dependency-vulnerabilities`, `outdated-dependencies`, `sarif-import` |
| `fact-producer` | `sdk.FactProducer` | engine | `sbom-import` |
| `reporter` | `sdk.Reporter` | engine | `html`, `json`, `sarif` |
| `platform` | `sdk.Platform` | engine (fix pipeline) | `gitlab`, `github` |
| `tool-resolver` | `sdk.ToolResolver` | toolchain | `config-path`, `wrapper`, `path`, `container`, `download` |
| `output-format` | `sdk.OutputFormat` | integrations | `sarif`, `cyclonedx` |
| `advisory-source` | `deps.AdvisorySource` | the `dependency-vulnerabilities` analyzer | `osv`, `osv-offline`, `ghsa` |

`pkg/sdk` is check-agnostic. Dependency-specific types (`Version`, the
dependency graph, advisory sources, version registries) live in
[`pkg/sdk/deps`](../pkg/sdk/deps).

## Facts

Analyzers do not compute their inputs. They declare the **facts** they need,
and the engine produces each fact at most once per project and shares it:

```go
func (a *Analyzer) Requires() []string { return []string{deps.FactDependencyGraph} }

graph, err := sdk.GetFact(ctx, req.Facts, deps.GraphKey)
```

Facts are produced by a standalone `FactProducer` plugin whose `AppliesTo`
returns true (for example SBOM import or a config-defined integration) or, if
there is none, by the project's ecosystem plugin. The engine provides the
`source-tree` fact for every project. If a required fact is unavailable, the
analyzer is skipped for that project with reason `fact-unavailable: <name>`.

Fact values that implement `sdk.ReportableFact` are embedded in the JSON
report (the dependency graph does this).

## Findings and remediations

Every analyzer returns `[]sdk.Finding`, one generic model modelled on SARIF
2.1.0. It carries the analyzer, rule, kind (`vulnerability`, `outdated`,
`license`, `code-issue` or your own), severity, message, locations (a file
region and/or a package with its dependency paths), references, aliases,
free-form `properties`, and optional remediations. Reports, the quality
gate, ignore rules and SARIF output handle every kind without changes.

A remediation is either a `PackageChange` (realized by the owning
ecosystem's `RemediationRealizer`) or a list of `TextEdit`s that the fix
pipeline applies directly. When there are several remediations, they are
alternatives in order of preference: the fix pipeline tries them in order and
publishes the first one that passes verification.

## Running external tools

Plugins never start processes directly (an architecture test enforces this).
They ask the toolchain:

```go
tool, err := host.Tools().Require(ctx, sdk.ToolRequirement{Name: "mvn", Constraint: ">=3.6"}, &project)
res, err := tool.Run(ctx, sdk.RunSpec{Args: []string{"-v"}, Dir: dir})
```

The toolchain resolves wrappers, `PATH`, containers or downloads, applies
per-tool settings, masks secrets and enforces timeouts. See
[toolchain.md](toolchain.md).

## Registration and custom binaries

Plugins register themselves at compile time:

```go
func init() { sdk.Register(New()) }
```

To ship your own plugins, build a custom binary that blank-imports the
built-in set plus yours:

```go
package main

import (
	"os"

	"github.com/deepward-oss/deepward/pkg/app"
	_ "github.com/deepward-oss/deepward/plugins/all" // built-in plugins
	_ "example.com/acme/deepward-plugins/hello"   // your plugins
)

func main() { os.Exit(app.Main(os.Args[1:])) }
```

External tools that cannot be compiled in can contribute findings through
SARIF import or config-defined integrations (see
[integrations.md](integrations.md)).

## Configuration

A plugin that implements `sdk.Configurable` returns a pointer to its settings
struct, pre-filled with defaults. The engine decodes `plugins.<id>` into it
and rejects unknown keys. Use `sdk.Duration`, `sdk.Date` and `sdk.Severity`
for typed values, and implement `sdk.Validator` for cross-field checks.

```yaml
plugins:
  hello-readme:
    severity: medium
```

## Example: a complete analyzer

[`examples/hello-analyzer`](../examples/hello-analyzer/hello.go) reports
projects without a README. It is about 60 lines and shows `Info`,
configuration, required facts and finding construction.

## Conformance kit

[`pkg/sdk/sdktest`](../pkg/sdk/sdktest) contains a test suite per plugin
kind (`TestAnalyzer`, `TestEcosystem`, `TestReporter`, `TestFactProducer`,
`TestOutputFormat`, `TestToolResolver`, `TestPlatform`). It checks:

- the ID format and `Info` stability;
- that the configuration round-trips and the defaults validate;
- determinism and context cancellation;
- that empty input causes no panics;
- that findings are valid and map to SARIF without loss.

It also provides in-memory fakes of every kind for your own tests. Use the kit
as the acceptance test of your plugin:

```go
func TestHelloConforms(t *testing.T) {
	sdktest.TestAnalyzer(t, hello.New(), req)
}
```
