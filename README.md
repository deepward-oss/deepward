# Deepward

Deepward is a universal checks orchestrator for CI. Its built-in plugins
find **vulnerable and outdated dependencies** (including transitive ones) in
**Maven, sbt, NuGet, npm/Yarn/pnpm, Go and Cargo** projects. It writes **HTML, JSON and SARIF**
reports and, if you ask, opens verified **GitLab merge requests and GitHub
pull requests**. Other checkers (static analyzers, linters, secret scanners)
are added through configuration, so one report, one quality gate and one set
of ignore rules cover them all.

```sh
deepward scan --fail-on high          # reports in deepward-out/, exit 1 on high+
deepward fix                          # dry run: verified edits as a diff
deepward fix --publish                # open/update merge or pull requests
```

- **The graph comes from your build tools.** Deepward runs Maven, sbt,
  dotnet and Go, and reads the lockfiles npm, Yarn, pnpm and Cargo wrote, so
  the resolved versions are exactly the ones your build uses. Nothing in the
  repository is modified.
- **Open vulnerability data, safe for commercial use.** OSV.dev (online or
  offline), and optionally the GitHub Advisory Database. Each source's data
  license is checked and credited in every report
  ([data-licenses.md](docs/data-licenses.md)).
- **Verified fixes.** Every edit is checked by rebuilding the graph before a
  request is opened. Transitive vulnerabilities are fixed by upgrading the
  direct dependency or by pinning.
- **Runs in any CI.** A single static binary or a 5 MB image. GitLab CI and
  GitHub Actions are detected automatically.
- **Pluggable.** Ecosystems, analyzers, advisory sources, reporters,
  platforms, tool resolvers and output formats are plugins behind a public
  SDK ([plugins.md](docs/plugins.md)).

## Installation

Download an archive for Linux, macOS or Windows (amd64 or arm64) from the
[releases page](https://github.com/deepward-oss/deepward/releases) and put
`deepward` on your `PATH`. Every release has a `checksums.txt` and SBOMs:

```sh
sha256sum --check --ignore-missing checksums.txt
```

The container image (linux/amd64 and linux/arm64) has only the static
binary. It can scan lockfile-based projects (npm, Yarn, pnpm, Cargo) as is.
Maven, sbt, .NET and Go need their build tools, so copy the binary into an
image that has them:

```sh
docker run --rm -v "$PWD:/src" -w /src ghcr.io/deepward-oss/deepward:0.2.1 scan
```

```dockerfile
COPY --from=ghcr.io/deepward-oss/deepward:0.1 /deepward /usr/local/bin/deepward
```

With Go 1.26 or later:

```sh
go install github.com/deepward-oss/deepward/cmd/deepward@v0.2.1
```

Versions follow SemVer; see [docs/versioning.md](docs/versioning.md) and the
[changelog](CHANGELOG.md).

## Documentation

| Topic | |
|-------|-|
| Commands, exit codes, CI detection | [docs/cli.md](docs/cli.md) |
| `deepward.yaml` reference | [docs/configuration.md](docs/configuration.md), [schema](schema/deepward.schema.json) |
| Maven, sbt, NuGet, npm/Yarn/pnpm, Go, Cargo specifics | [docs/ecosystems/](docs/ecosystems) |
| Tools, wrappers, containers, downloads | [docs/toolchain.md](docs/toolchain.md) |
| Add-ons, setup steps, config-only integrations | [docs/integrations.md](docs/integrations.md) |
| Fixes and merge/pull requests, CI setups | [docs/publishing.md](docs/publishing.md) |
| Writing plugins | [docs/plugins.md](docs/plugins.md) |
| Vulnerability data licenses | [docs/data-licenses.md](docs/data-licenses.md) |
| Versions, compatibility, releases | [docs/versioning.md](docs/versioning.md), [CHANGELOG.md](CHANGELOG.md) |

## Development

```sh
make build        # bin/deepward
make test         # unit, doc and end-to-end tests
make cover-check  # coverage gates: >=95% domain/app, >=85% adapters, >=90% overall
make lint         # golangci-lint, including module-boundary rules
make licenses     # dependencies must be MIT-compatible
make integration  # real Maven, sbt, .NET SDK, Node.js, Go and Rust in Docker (needs Docker and network)
make dist         # dist/ binaries for linux/amd64, linux/arm64, darwin/arm64, windows/amd64
make snapshot     # local GoReleaser run: archives, checksums, SBOMs, image (needs goreleaser, syft, Docker)
make image        # container image built from source
```

The requirements are in [openspec/specs](openspec/specs). The proposals and
design decisions behind them are in
[openspec/changes/archive](openspec/changes/archive).

## License

MIT, see [LICENSE](LICENSE). Authors are listed in [AUTHORS](AUTHORS).
