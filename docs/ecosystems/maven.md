# Maven

The `maven` ecosystem plugin handles Maven projects.

## Detection

Every `pom.xml` that is not a `<module>` of another detected POM is the root
of a project. Its modules are followed recursively, including modules
declared in profiles. A reactor with `core` and `web` modules is therefore one
project (`maven:.`), and the build tool runs once for it, not once per module.

## Dependency graph

The graph comes from Maven itself, so conflict resolution ("nearest wins",
`dependencyManagement`, exclusions) is exactly what your build uses:

```text
mvn -B -q -f <project>/pom.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.8.1:tree \
  -DoutputType=json -DoutputFile=<tmp> -DappendOutput=true
```

The plugin goal is invoked by full coordinates, so your POM is not modified
and the plugin is downloaded through your normal mirror. The module trees are
merged into one project graph. Reactor modules are not graph nodes. Each
direct dependency records the modules that declare it (`modules` attribute).

Requirements: Maven 3.6.3 or later (the default `maven-dependency-plugin`
3.8.1 needs it). Use `mvnw` in the repository, or see [toolchain.md](../toolchain.md).

### Add-ons

| Name | Kind | Default |
|------|------|---------|
| `dependency-plugin` | `maven-goal` | `org.apache.maven.plugins:maven-dependency-plugin:3.8.1:tree` |

If your mirror does not have 3.8.1, use an older version that still supports
JSON output (3.7.0 or later):

```yaml
ecosystems:
  maven:
    addons:
      dependency-plugin:
        version: 3.7.1
```

## Private repositories

- **Resolution** uses your usual Maven configuration. Pass a `settings.xml`
  with `DEEPWARD_MAVEN_SETTINGS=ci/settings.xml`, `plugins.maven.settings`, or
  `tools.mvn.args: ["-s", "ci/settings.xml"]`.
- **Version discovery** (for outdated dependencies and fixes) reads
  `maven-metadata.xml` from the repositories below, in order. Version lists
  from several repositories are merged. Credentials come from environment
  variables, never from the file:

```yaml
plugins:
  maven:
    settings: ci/settings.xml
    cacheTTL: 6h
    repositories:
      - id: nexus
        url: https://nexus.acme.local/repository/maven-public
        usernameEnv: NEXUS_USER
        passwordEnv: NEXUS_PASSWORD
      - id: central
        url: https://repo.maven.apache.org/maven2
```

A repository that answers 5xx is retried three times. If it still fails,
affected updates are reported as unknown and the scan continues.

## Fixing transitive vulnerabilities

When a vulnerable package comes in transitively, deepward looks for an
upgrade of a direct dependency that brings a fixed version. It reads the
published POMs of the newer versions of that dependency from the same
repositories (with their parent POMs, properties, `dependencyManagement` and
imported BOMs) and follows the dependency chain down to the vulnerable
package. The report then names the oldest version that brings a fix, for
example:

> upgrade org.apache.kafka:kafka-clients from 4.4.0 to 4.5.0: it brings
> at.yawk.lz4:lz4-java 1.11.4, which fixes these advisories (or pin
> at.yawk.lz4:lz4-java to 1.11.4)

The closest direct dependency is checked first: when a package is reachable
through several direct dependencies, the one with the shortest chain decides
its version. If no released version brings a fix, the recommendation is to pin
the transitive version in the `dependencyManagement` of the root POM. If the
direct dependency is not in any configured repository (an internal library),
the report says so: add the repository that publishes it under
`plugins.maven.repositories`. The POMs only tell what a version asks for, so
`deepward fix` still rebuilds the dependency graph to verify the change
against the whole project.

## Failure handling

- If `mvn` is missing or too old, Maven projects are skipped with reason
  `tool-not-found` / `tool-version-mismatch`.
- If the build fails (for example an unresolvable dependency), the project is
  marked failed and the report shows the last 200 lines of Maven output.
  Secrets from the environment are masked.
