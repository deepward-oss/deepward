# Design

## Context

Greenfield repository: only the OpenSpec scaffold exists. Motivation and scope are in proposal.md, and behavior contracts are in `specs/`. Constraints that shape the design:

- Target projects at work use sbt, Maven and NuGet, often with private registries (Nexus/Artifactory, internal NuGet feeds).
- The tool must run in any CI (GitLab CI, GitHub Actions, Jenkins, …), so it must be a non-interactive CLI with no runtime prerequisites besides the build tools the project already needs.
- Everything compiled into the binary must be permissively licensed. Vulnerability data licenses are a separate concern.
- High test coverage is a hard requirement, so the architecture must keep logic testable without real build tools or network.

## Goals / Non-Goals

**Goals:**
- Clean DDD modularity: bounded contexts with pure domain cores and replaceable adapters (ports and adapters).
- One code path for graph-based analysis no matter where the graph came from (build tool or imported SBOM).
- Deterministic, reproducible outputs, so golden-file tests are possible.
- Read-only by default. Writing to a remote happens only through a `Platform` plugin invoked by the fix pipeline.
- A universal core: nothing in `pkg/sdk` or `internal/engine` is dependency-specific. Dependency concepts (graph, purl ranges, advisories) live in facts and plugins, so non-dependency checks can join through the same contracts.

**Non-Goals:**
- Implementing our own static analyzers, linters or secret scanners. Those checks are integrated as external tools (`integrations`, SARIF import) only.
- Reimplementing dependency resolution of any ecosystem.
- A long-running server, a web UI or a database. State lives in the hosting platform (MR/PR metadata) and in the local cache.
- Auto-merge, dashboards, Gradle/npm (later changes).
- Sandboxing the target build. It runs with the same trust as the project's own CI build (see Risks).

## Decisions

### D1. Language: Go
Go produces a single static cross-compiled binary that is trivial to drop into any CI image. It also has first-class permissive libraries in this domain: `github.com/google/osv-scalibr` (Apache 2.0) ships the same Maven and NuGet version comparators that OSV uses for its ranges, which removes a whole class of matching bugs. `html/template` gives context-aware auto-escaping for the report.
*Alternative: Node.js.* Rejected: it needs a runtime or a heavy bundler (pkg/SEA) in CI images, and has no equivalent of the OSV semantic version library.

### D2. Microkernel architecture: a small engine plus plugins
The core is a **run engine** that knows only contracts. Everything that does real work is a plugin. This is what lets new analyzers, including future static code analyzers, plug in by implementing one interface.

**Public SDK (`pkg/sdk`, versioned, stable).** This is the only package plugins import from the core:
- `sdk.Plugin`: `ID()`, `Kind()`, `Version()`, `Describe()`, `ConfigSchema()`, `Configure(raw)`.
- Kinds and their contracts:
  - `Ecosystem`: `Detect(fs) []ProjectRef`. It can also implement capability interfaces, discovered by Go type assertion, so an ecosystem implements only what it supports: `GraphProvider`, `VersionRegistry`, `ManifestEditor`.
  - `Analyzer`: `Requires() []FactKey`, `Analyze(ctx, Scope) ([]Finding, error)`. `Scope` gives read access to facts for one project, or for the whole workspace for workspace-level analyzers.
  - `AdvisorySource`: `Query(ctx, []PURL) ([]Advisory, error)` and `DataLicense()`. It is a sub-plugin consumed by the `dependency-vulnerabilities` analyzer, so new vulnerability databases are plugins too.
  - `Reporter`: `Render(ctx, ScanResult, io.Writer)`.
  - `Platform`: change-request operations (find, create, update, close, comment) and `Commit(files)`.
  - `FactProducer`: `Produces() FactKey`, `AppliesTo(ProjectRef)`, `Produce(ctx, Scope)`. Ecosystem graph providers and SBOM import are fact producers.
  - `ToolResolver`: `Resolve(ctx, ToolRequirement, ProjectRef) (ToolCandidate, error)` (see D12).
  - `OutputFormat`: `Parse(io.Reader) (Findings | Fact)`. It parses tool output for config-defined integrations; `sarif` and `cyclonedx` are built in.
- **Tools service (`sdk.Tools`)**, injected into every plugin call: `Require(ToolRequirement) (Tool, error)` and `Tool.Run(ctx, args, opts) (Result, error)`. It is the only way a plugin can start a process.
- **Facts:** a typed key/value store (`FactKey[T]`). Fact producers are registered by key: `dependency-graph` is produced by the project's ecosystem `GraphProvider` or by SBOM import, and `source-tree` is a read-only view of the project files. Facts are computed lazily, at most once per project, and memoized. A future `ast:<lang>` fact for static analyzers fits the same mechanism.
- **Finding model:** analyzer, rule, kind, severity, message, locations (file region and/or package location with purl and path), references, fingerprint, properties, remediations. It is modelled on SARIF 2.1.0 so mapping is lossless in both directions.
- **Remediation:** `PackageVersionChange{project, purl, version}` | `TextEdit{file, span, replacement}`.
- Shared value types: `PURL`, `Version` (+ comparators), `Ecosystem`, `Severity`, `Clock`.

**Engine (`internal/engine`).** Plugin registry, config binding (`plugins.<id>` validated against each plugin's schema), fact store, scan pipeline (detect → produce facts → run analyzers concurrently with a bounded worker pool → apply ignore rules → evaluate gate → render), fix pipeline (collect remediations → group → realize through `ManifestEditor`/text edits → verify by fact recomputation → publish through `Platform`). Every plugin call is wrapped with timeout plus `recover()` for fault isolation.

**Registration.** Plugins register themselves at compile time (`sdk.Register(...)` in `init()`, like `database/sql` drivers). `cmd/deepward` blank-imports the built-in set. A third party can build a custom binary from its own `main` importing `pkg/sdk` plus its plugins, the same model as `xcaddy`. External tools that cannot be compiled in contribute via the `sarif-import` analyzer.
*Alternatives:* Go's `plugin` package (rejected: Linux/macOS only, fragile across toolchain versions) and HashiCorp go-plugin over gRPC (MPL-2.0, adds complexity). An out-of-process **exec plugin protocol** (JSON over stdin/stdout, contracts identical to `pkg/sdk`) is reserved as a follow-up. The SDK is designed so it can be added without changing any contract.

**Module layout.** Each plugin is its own bounded context with a hexagonal structure inside:
```
cmd/deepward/                      main: blank-import built-in plugins, run cli
pkg/sdk/                           public plugin API (contracts, finding model, value types)
internal/engine/                   registry, config binding, fact store, scan & fix pipelines, gate
internal/cli/                      cobra commands, config layering, CI detection, exit codes
internal/toolchain/                implements sdk.Tools: resolver chain, version probes, per-tool settings,
                                   setup steps, process runner (timeouts, ring buffer, secret masking, env allowlist)
internal/integrations/             turns config-defined command analyzers / fact producers into plugins
internal/textedit/                 span-based edit primitives shared by editors
plugins/
  ecosystem/maven|sbt|nuget/       each: domain/ (manifest model, property resolution)
                                         app/ (detect, graph, versions, edit)
                                         adapters/ (mvn|sbt|dotnet invocation, registry HTTP, file editors)
  fact/sbom/                       CycloneDX import as an alternative dependency-graph producer
  analyzer/depvuln/                domain/ (Advisory, AffectedRange, Deduplicator, SeverityPolicy, FixSelector)
                                   app/ (analyze graph → findings + remediations)
  analyzer/outdated/               domain/ (UpdatePolicy, classification) app/
  analyzer/sarifimport/
  advisory/osv|osvoffline|ghsa/
  reporter/html|json|sarif/
  platform/gitlab|github/
  toolresolver/configpath|wrapper|path|container|download/
  outputformat/sarif|cyclonedx/
```

**Dependency rules** are enforced by an architecture test (`internal/archtest`, walking `go list -deps`) plus `depguard`:
- `pkg/sdk` imports only the standard library (and osv-scalibr `semantic` for version comparison).
- `internal/engine` imports `pkg/sdk` only, never `plugins/...`.
- `plugins/<kind>/<name>` imports `pkg/sdk` and `internal/textedit`, never another plugin, and never `os/exec`. Processes are started only through `sdk.Tools`, so every tool invocation gets resolution, settings, masking and timeouts. Collaboration happens only through facts and SDK contracts. For example, `depvuln` gets advisory sources from the registry by kind, not by import.
- Inside a plugin: `domain` → standard library + `pkg/sdk` value types only. No I/O.

### D3. Graph acquisition: let the build tool resolve, never modify the project's build files
- **Maven:** `mvn -B -q org.apache.maven.plugins:maven-dependency-plugin:3.8.1:tree -DoutputType=json -DoutputFile=<tmp> -Dscope=...` invoked by full coordinates, so the POM is not touched and the plugin comes through the corporate mirror. A reactor build yields one tree per module, and they are merged into one project graph.
  *Alternative: cyclonedx-maven-plugin.* It is still supported through the SBOM import path, but is not the default: its output flattens the per-module structure we need for edits.
- **sbt:** an embedded tiny sbt plugin source (about 80 lines of Scala, shipped in the binary via `embed`) is injected with `--addPluginSbtFile`, the same technique Scala Steward uses. It writes the `updateFull` report per configuration and cross Scala version as JSON. This avoids depending on a third-party plugin version and works on sbt 1.4+. The build itself is not modified.
  *Alternatives: the built-in `dependencyTree` (text output, fragile parsing) and sbt-bom (extra plugin resolution, less control over scopes).*
- **NuGet:** `dotnet restore` (respects `nuget.config`, CPM), then parse `obj/project.assets.json`, which contains the fully resolved graph per target framework. `dotnet list package --include-transitive` is not used because its text output is unstable across SDK versions.
- **All tools** run through `sdk.Tools` (D12): timeouts, output capture with a ring buffer (last 200 lines), secret masking, and an environment allowlist. `DEEPWARD_*_TOKEN` variables are **removed** from the child environment.
- Every Maven plugin goal, injected sbt file and dotnet tool used above is declared as a named **add-on** with a default version (for example `dependency-plugin: 3.8.1`), so configuration can override or extend it (D12).

### D4. Version semantics live in the SDK and are reused everywhere
`sdk.Version` wraps the osv-scalibr `semantic` parsers for Maven and NuGet. The same comparator backs range matching (advisory), update classification (updates) and fix selection (engine fix pipeline). Scala cross suffixes are handled in `sdk.PURL` (base name + binary suffix).

### D5. Vulnerability sources
- **OSV API** (`/v1/querybatch`, then `/v1/vulns/{id}` for details) is the primary source. OSV already aggregates GHSA for Maven and NuGet.
- **GHSA REST** (`/advisories`) is an optional second source used to cross-check and fill CVSS gaps. It needs a GitHub token only for rate limits.
- **Offline:** per-ecosystem OSV exports (`.../Maven/all.zip`, `.../NuGet/all.zip`) are loaded into an in-memory index keyed by package name. `db update` downloads them with ETag checks.
- **Licensing:** each `AdvisorySource` adapter declares a `DataLicense` value. The source registry refuses to enable a source whose license is not on the allowlist. CC-BY-4.0 data (GHSA) needs attribution, which goes into the report footer and the SARIF tool metadata.

### D6. Fix strategy for transitive findings: bounded trial by rebuild
Computing which ancestor version brings a fixed transitive dependency is not solvable statically without reimplementing resolution. Instead, the `depvuln` analyzer emits a remediation for the direct ancestor, and the engine fix pipeline takes up to N (default 5) ancestor candidate versions in ascending order, from the nearest above current, applies the edit in a scratch copy, and recomputes the `dependency-graph` fact through the ecosystem plugin. The first candidate that removes the advisory wins. If none does, it falls back to pinning (Maven `dependencyManagement`, NuGet CPM/`PackageReference`). The same rebuild is the verification step required before publishing. Rebuilds are cached by manifest content hash.

### D7. Span-based manifest editing
Editors find the exact byte span of a version token and replace only that span. Files are never re-serialized.
- **POM / MSBuild:** `encoding/xml` decoder with `InputOffset()` locates element text spans. Property resolution follows `${...}` within the repository's POMs, including parents reachable by relative path.
- **sbt:** a small Scala-aware tokenizer (strings, triple-quoted strings, comments) plus patterns for `"g" %% "a" % "v"`, `"g" % "a" % versionVal` and `val versionVal = "v"`. This follows the heuristic Scala Steward proved in practice, with no Scala parser needed. When the result is ambiguous (several candidate literals), the editor returns `manual`.
- Line endings and encoding (including BOM) are preserved, and there is a property test: the edit leaves only the targeted byte span changed.

### D8. Publishing through platform APIs, not git push
Commits are created through the GitLab Commits API (multi-file actions) and the GitHub Git Data API (blobs → tree → commit → ref update). This needs only the API token, no git credential setup in CI, and works identically from any CI. Branch existence, human-authored commits, the open MR/PR lookup (by branch plus bot marker) and limit counting all go through the same `Platform` port. The plugins call the REST APIs directly with `net/http` (a dozen endpoints each), which avoids two large client libraries and keeps them testable against recorded `httptest` servers. Commits carry a fixed bot author (`sdk.BotAuthor`), so commits by anyone else are detected reliably on both platforms, including GitHub App installation tokens that cannot call `/user`.
*Alternative: local git plus push.* Rejected: credential handling differs for every CI.

### D9. Reporting from one canonical model
`sdk.ScanResult` is the single serializable result (projects, facts summary, findings of every kind, plugin runs, warnings, metadata), and JSON is its direct encoding with `schemaVersion: 1`. HTML and SARIF reporters are pure functions of `ScanResult`. The HTML reporter has dedicated views for kinds `vulnerability` and `outdated` and a generic view for any other kind, so new analyzers appear in reports automatically. HTML uses `html/template` with embedded CSS and vanilla JS (no framework, no CDN). Tables are rendered server-side, so the report stays readable without JS, and the JS only adds filter, sort and search. Determinism comes from sorting every collection by a stable key and injecting the clock.

### D10. CLI and configuration
`spf13/cobra` (Apache 2.0) handles commands. Configuration uses `gopkg.in/yaml.v3` with `KnownFields(true)` for unknown-key errors and line numbers, layered with env and flags in `internal/cli`; plugin sections are delegated to each plugin's schema by the engine. A published JSON Schema for `deepward.yaml` gives editor completion. CI detection is a table of providers (GitLab, GitHub, then generic flags).

### D11. Testing strategy ("fully covered")
- **Domain and app:** table-driven unit tests with in-memory fakes for every port. Coverage gate is **≥ 95% per package**.
- **Adapters:** fixture-based tests with recorded build-tool outputs (`testdata/` with real `dependency:tree` JSON, sbt plugin JSON, `project.assets.json`) and `httptest` servers that replay recorded registry, OSV, GitLab and GitHub responses. Coverage gate is **≥ 85%**.
- **Plugin conformance kit (`pkg/sdk/sdktest`):** a public test suite per plugin kind (ID format, config schema round-trip, determinism, context cancellation, no panics on empty input, finding-model validity, SARIF mappability). Every built-in plugin and every in-memory fake runs it, and third-party plugins can use it as their acceptance test.
- **Engine tests with fake plugins:** the scan and fix pipelines, fact memoization, fault isolation (panicking or timing-out plugins) and gate evaluation are tested entirely with fake plugins. This also proves the engine has no dependency on concrete plugins.
- **Golden files:** JSON, SARIF and HTML reports are compared against goldens (`-update` flag to regenerate). SARIF is additionally validated against the official 2.1.0 schema.
- **Property and fuzz tests:** version comparison (consistency against osv-scalibr), span editors (only the targeted span changes), the sbt tokenizer (`go test -fuzz`).
- **Integration tests** (`//go:build integration`): Docker images with real sbt, Maven and .NET SDK scan sample projects under `testdata/projects/`. They run in CI on every merge request.
- **E2E:** the CLI binary runs against fixture repos plus fake platform servers and asserts exit codes, outputs and API calls.
- The overall coverage gate (**≥ 90%**) and a license check (`go-licenses check` with an allowlist of MIT, BSD-2/3, Apache-2.0, ISC and MPL-2.0 only if unmodified) run in the tool's own CI.

### D12. Toolchain: declared tools, resolver chain, add-ons, integrations
Projects at work need different tool versions, wrappers, corporate settings and extra build-tool plugins, and the same will be true for future analyzers. All of that goes through one subsystem instead of per-plugin code.

- **Tool catalog.** A tool is a logical name (`mvn`, `sbt`, `dotnet`, `java`, `git`, or a user-defined name like `semgrep`) with a **version probe** (command plus regex, for example `mvn -v` → `Apache Maven (\S+)`). Built-in tools ship with probes. User-defined tools declare theirs in `tools.<name>`.
- **Resolver chain.** `ToolResolver` plugins are tried in configured order per tool (default `config-path → wrapper → path`, while `container` and `download` are opt-in). A resolver returns a `ToolCandidate` (an invocation prefix such as `["./mvnw"]` or `["docker","run",...,"image","sbt"]`, plus env), the probe checks the version constraint, and the first match wins. Results are cached per run and per project (wrappers are project-local). Adding a new provisioning method (for example SDKMAN, asdf, mise) means adding one resolver plugin.
- **Per-tool settings** (`tools.<name>.{path,resolvers,image,download,args,env,javaHome,jvmOpts,workdir,timeout}`) are merged into every invocation by `internal/toolchain`. Env values can reference CI variables (`${env:NEXUS_TOKEN}`), which are automatically registered as secrets for masking.
- **Add-ons.** Ecosystem plugins expose named add-ons through the SDK (`AddonSpec{name, kind: maven-goal|sbt-plugin-file|dotnet-tool, default}`). The resolved add-on set (defaults overlaid with `ecosystems.<id>.addons`) is passed to the plugin, which renders it into the invocation: a full-coordinate goal, `--addPluginSbtFile=<file>`, or `dotnet tool restore` + `dotnet tool run`. The project's build files are never edited.
- **Setup steps** (`ecosystems.<id>.setup`, `projects.<path>.setup`) run through the same `sdk.Tools` before the fact producers of the matching projects. Their failures are attached to those projects only.
- **Config-defined integrations** (`integrations.<id>`) are compiled at startup into ordinary `Analyzer` or `FactProducer` plugins by `internal/integrations`: they render args from a small `text/template` context (`ProjectDir`, `OutputFile`, `Ecosystem`, `RepoRoot`), run the tool, and parse the output file through a registered `OutputFormat`. Because they are real plugins, IDs, enabling, timeouts, isolation, `plugins list` and reporting apply to them unchanged. This is the zero-code path for semgrep, the CycloneDX Maven or dotnet tools, osv-scanner and similar tools.
- **`deepward doctor`** asks the engine for every enabled plugin's and integration's tool requirements, runs only resolution plus probes, and prints a table.
*Alternatives:* each ecosystem finding its own binaries (rejected: duplicated logic, no single place for wrappers, containers or corporate settings) and always running tools in containers (rejected as the default: many CI runners have no Docker socket, but kept as an opt-in resolver).

## Risks / Trade-offs

- [Config-defined integrations and setup steps run arbitrary commands from `deepward.yaml`] → They have the same trust as the repository's CI script, which can already run anything. Integrations get the same env allowlist and token stripping as built-in tools. `doctor` and `--dry-run` print the exact commands.
- [The container resolver changes file ownership and paths (mounted workspace, UID mismatch)] → Run with the host UID/GID, mount at the same absolute path, document the limitations, and keep it opt-in.
- [Wrappers (`mvnw`) download Maven themselves and may be blocked behind a proxy] → The resolver order is configurable per tool. `doctor` shows which resolver was chosen and why the others were skipped.

- [Running the target build executes project code (sbt builds, Maven plugins, MSBuild targets)] → Run `deepward` in the project's own CI job with the same trust as its build. Strip platform tokens from the child environment. The docs recommend a separate job with a write token only for `fix --publish`.
- [The public SDK becomes a compatibility burden once third parties build against it] → Mark `pkg/sdk` as `v0` (unstable) during the MVP, keep contracts minimal and grow them through optional capability interfaces instead of changing existing methods, and track the SDK version in `plugins list` output.
- [sbt startup and resolution are slow (minutes on large builds), and trial rebuilds multiply that] → Cap candidates (D6), cache rebuild results by manifest hash, reuse the Coursier cache between CI runs (documented cache paths).
- [The sbt literal heuristic can miss or pick the wrong literal] → Prefer `manual` over guessing whenever there is ambiguity. Verification by rebuild catches wrong edits before publishing.
- [`maven-dependency-plugin` JSON output may be missing on mirrors pinned to old versions] → The plugin version is configurable, and the fallback is SBOM import via cyclonedx-maven-plugin.
- [OSV aggregates sources with different licenses] → Use only Maven and NuGet ecosystems, keep the source's `database_specific`/license metadata, and show attribution. Review data licenses periodically (task in tasks.md).
- [API-based commits can't run repository hooks (pre-commit formatting)] → Edits are minimal spans, so formatting does not change. The project's CI pipeline on the MR remains the gate.
- [GitHub Git Data API creates unsigned commits] → Documented. Signed-commit enforcement on target branches needs a GitHub App later.

## Migration Plan

Not applicable: this is a new tool with no existing users. Rollout: publish binaries and image, then adopt in one pilot repository per ecosystem in report-only mode (`scan`), and only then enable `fix --publish` per repository.

## Open Questions

- The final default for the CVSS v4 vs v3 preference when both exist (currently: the higher score wins). This can be tuned later without changing specs.
- Whether to also publish a GitLab CI component and a GitHub Action wrapper in this change or the next one. Packaging only, no behavior change.
