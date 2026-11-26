# sbt

The `sbt` ecosystem plugin handles sbt builds.

## Detection

A directory with `build.sbt` or `project/build.properties` is the root of a
build, unless it lies inside another build. Subproject `build.sbt` files and
the meta-build files (`project/*.scala`, `project/*.sbt`,
`project/build.properties`) are the project's manifests.

## Dependency graph

Deepward does not parse build files to find dependencies. It asks sbt:

1. It writes a small sbt `AutoPlugin` (embedded in the Deepward binary) into
   `<build>/target/deepward/src`.
2. It passes a one-line file to sbt with `--addPluginSbtFile`. The file adds
   that directory to the meta-build sources.
3. It runs `sbt -batch +deepwardGraph`. For every subproject and cross Scala
   version, the task writes the `updateFull` report (resolved modules and
   their callers per configuration) as one JSON line.
4. It deletes the temporary files.

Nothing in the repository is modified, and no third-party sbt plugin is
resolved. Evicted modules are dropped. Modules of the build itself (for
example `core` used by `root`) are not graph nodes. Scala artifacts keep
their binary suffix (`cats-core_2.13`), so updates are only looked up for the
same Scala version (`cats-core_3` is never proposed for a 2.13 build).

Requirements: an sbt launcher 1.4 or later (`--addPluginSbtFile`). The build
itself can use any sbt 1.x version.

### Add-ons

| Name | Kind | Default |
|------|------|---------|
| `graph-dump` | `sbt-plugin-file` | built-in `DeepwardGraphPlugin.scala` |

Extra `sbt-plugin-file` add-ons are passed as further `--addPluginSbtFile`
arguments, for example to add corporate resolvers to the meta-build:

```yaml
ecosystems:
  sbt:
    addons:
      corp-resolvers:
        kind: sbt-plugin-file
        file: ci/corp-resolvers.sbt
plugins:
  sbt:
    crossBuild: true
    repositories:
      - id: nexus
        url: https://nexus.acme.local/repository/maven-public
        usernameEnv: NEXUS_USER
        passwordEnv: NEXUS_PASSWORD
```

## Performance

sbt startup and resolution take time: tens of seconds for small builds,
minutes for large ones. In CI, cache these directories between runs:

| Cache | Path |
|-------|------|
| Coursier | `~/.cache/coursier` (Linux), `~/Library/Caches/Coursier` (macOS) |
| Ivy | `~/.ivy2/cache` |
| sbt | `~/.sbt` |

To run sbt in a container instead of on the runner, see the `container`
resolver in [toolchain.md](../toolchain.md).
