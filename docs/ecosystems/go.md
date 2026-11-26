# Go modules

The `go` ecosystem plugin handles Go modules.

## Detection

Every `go.mod` is a project. Like the `go` command, Deepward skips `vendor`
and `testdata` directories and directories starting with `.` or `_`. The
project's manifests are `go.mod` and `go.sum`. Go workspaces (`go.work`) are
ignored: each module is scanned on its own (`GOWORK=off`).

## Dependency graph

The graph comes from the Go toolchain:

```text
go list -m -json all      # selected versions, Indirect flags, replacements
go mod graph              # requirement edges
```

Both run with `-mod=readonly`, so `go.mod` and `go.sum` are never modified.
Edges from versions that minimal version selection did not pick are dropped,
and every edge points to the selected version. Modules marked `// indirect`
are not direct dependencies. A module replaced by a local directory is part
of the project. A module replaced by another module appears under the
replacement's path and version, with a `replaces` attribute.

### Which modules are built

The module graph also contains modules whose packages your code never
imports. A dependency's `go.mod` requires the dependencies of *all* its
packages, for example every database driver of a migration library. To keep
those out of the report, Deepward lists the packages your module actually
compiles, without building anything:

```text
go list -e -deps       -f '{{with .Module}}{{.Path}}{{end}}' ./...   # build
go list -e -deps -test -f '{{with .Module}}{{.Path}}{{end}}' ./...   # build and tests
```

Both commands run for every target platform, and each module gets a scope:

| Scope | Meaning |
|-------|---------|
| `compile` | a package of the module is built for at least one platform |
| `test` | only tests need it |
| `not-built` | it is only in the module graph |

Advisories on `not-built` modules are still reported, with their real
severity and fixes, but marked **not built**:

- they do not fail the quality gate;
- they are shown in their own "Not built" section of the HTML report;
- `deepward fix` pins them with `go get` in change requests titled
  "(not built)" that never bypass the request limit.

The modules are in your `go.mod`/`go.sum`, so other scanners see them too,
which is why they are worth updating. See `notBuilt` in
[configuration.md](../configuration.md) to list them in a warning instead.
If no listing succeeds, every module keeps scope `compile`.

The default platforms are linux/amd64, darwin/arm64 and windows/amd64. Set
the platforms and build tags your releases use:

```yaml
plugins:
  go:
    platforms: [linux/amd64, linux/arm64]
    tags: [netgo, production]
```

This is module-level filtering: a module counts as built if any of its
packages is compiled. For call-level reachability, add `govulncheck` as an
[integration](../integrations.md).

Requirements: Go 1.17 or later, and access to your module proxy.

## Versions

Versions come from the module proxy protocol (`<proxy>/<module>/@v/list`)
through the proxies in `GOPROXY` (default `https://proxy.golang.org`).
A `,` separator moves to the next proxy only after 404/410, a `|` separator
after any error, and `direct` or `off` ends the list. Modules matching
`GONOPROXY`, or `GOPRIVATE` when `GONOPROXY` is unset, are never sent to a
proxy, and their updates are not checked. Credentials can be put in the
proxy URL (`https://user:token@goproxy.acme.local`). Override the
environment for Deepward only with:

```yaml
plugins:
  go:
    proxy: https://goproxy.acme.local,https://proxy.golang.org
    private: git.acme.local/*
```

Only versions of the same module path are proposed. A new major version with
a new import path (`/v2`) is never an automatic update.

## Fixes

Every change, direct or transitive, runs:

```text
go get <module>@<version>
```

`go.mod` and `go.sum` are updated, and the change is verified by rebuilding
the graph. Modules with a `replace` directive, and modules that vendor their
dependencies (`vendor/modules.txt`), are reported as manual changes with the
commands to run.
