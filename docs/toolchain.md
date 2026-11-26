# Toolchain

Deepward runs real build tools (`mvn`, `sbt`, `dotnet`, `go`, and `npm`,
`yarn`, `pnpm`, `cargo` for fixes) and, through integrations, any other CLI. Plugins never start processes themselves. They
declare the tools they need, and the toolchain:

1. finds a runnable tool through an ordered chain of **resolvers**;
2. probes its version and checks the plugin's constraint (for example `sbt >=1.4`);
3. applies the per-tool settings below to every invocation;
4. runs it with a timeout, keeps the last 200 lines of output for diagnostics
   and masks secrets.

Tools inherit only an allowlisted part of the environment (`PATH`, `HOME`,
locale, proxies, `JAVA_*`, `MAVEN_*`, `SBT_*`, `COURSIER_*`, `DOTNET_*`,
`NUGET_*`, `NODE_*`, `npm_config_*`, `YARN_*`, `PNPM_*`, `COREPACK_*`,
`GOPROXY`, `GOPRIVATE`, `GOFLAGS` and other Go settings, `CARGO_*`,
`RUSTUP_*`, …). `DEEPWARD_*_TOKEN` variables are never passed to tools. Pass
anything else explicitly with `tools.<name>.env`.

`deepward doctor` shows which resolver found each tool, its version, and why
the other resolvers were skipped.

## Resolvers

| ID | Default | What it does |
|----|---------|--------------|
| `config-path` | yes | Uses `tools.<name>.path` (relative paths are resolved from the repository root) |
| `wrapper` | yes | Uses project wrappers such as `mvnw` or `sbtx`, looking in the project directory and its parents |
| `path` | yes | Finds the executable on `PATH` |
| `container` | opt-in | Runs the tool inside `tools.<name>.image` (docker or podman) with the workspace mounted at the same path |
| `download` | opt-in | Downloads `tools.<name>.download.url`, verifies its SHA-256 and caches it |

The default order is `config-path → wrapper → path`. Setting `image` or
`download` adds the opt-in resolver at the end. `resolvers` sets the order
explicitly:

```yaml
tools:
  mvn:
    resolvers: [path, wrapper]      # ignore mvnw, prefer the CI image's Maven
  dotnet:
    path: /opt/dotnet-8/dotnet
  sbt:
    resolvers: [container]
    image: sbtscala/scala-sbt:eclipse-temurin-17.0.10_7_1.10.1_2.13.14
```

A download without a checksum is refused before anything is fetched:

```yaml
tools:
  osv-scanner:
    resolvers: [download]
    download:
      url: https://github.com/google/osv-scanner/releases/download/v2.0.0/osv-scanner_linux_amd64
      sha256: 0000000000000000000000000000000000000000000000000000000000000000
```

The container resolver runs as your user ID (`containerUser: host`, the
default) and mounts only the repository. Use `containerUser: image` to run as
the image's own user, or set an explicit `uid:gid`. The .NET SDK images need
`image`, because the SDK's first-run setup fails for user IDs it does not
know:

```yaml
tools:
  dotnet:
    resolvers: [container]
    image: mcr.microsoft.com/dotnet/sdk:8.0
    containerUser: image
```

To reuse a dependency cache between runs, point the tool to a directory inside
the repository (for example `-Dmaven.repo.local=.m2` or
`COURSIER_CACHE=.coursier`) and cache that directory in CI.

## Per-tool settings

| Key | Meaning |
|-----|---------|
| `args` | Arguments added to every invocation |
| `env` | Variables for the tool. `${env:NAME}` copies a CI variable, and its value is masked in logs and reports |
| `javaHome` | Sets `JAVA_HOME` and puts its `bin` first on `PATH` |
| `jvmOpts` | JVM options. They go to `MAVEN_OPTS` for mvn, `SBT_OPTS` for sbt and `JAVA_OPTS` for other tools |
| `workdir` | Working directory relative to the repository root, if the plugin does not set one |
| `timeout` | Per-invocation timeout (default `30m`) |
| `executables`, `versionArgs`, `versionPattern` | Define a tool Deepward does not know yet |

```yaml
tools:
  mvn:
    args: ["-s", "ci/settings.xml", "-Dmaven.repo.local=.m2"]
    env:
      NEXUS_USER: ci-bot
      NEXUS_TOKEN: ${env:NEXUS_TOKEN}
    jvmOpts: "-Xmx2g"
    javaHome: /usr/lib/jvm/temurin-17
    timeout: 20m
  semgrep:
    executables: [semgrep]
    versionArgs: [--version]
    versionPattern: '(\d+\.\d+\.\d+)'
```

## Version constraints

Plugins and integrations state constraints such as `>=1.4`, `>=3.6, <4` or
`=8.0.100`. When a resolver finds a tool whose version does not match, the
next resolver is tried. If none matches, the dependent projects are skipped
with reason `tool-version-mismatch` (for example `sbt 1.3.13 does not satisfy
>=1.4`).
