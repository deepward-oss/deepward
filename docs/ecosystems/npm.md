# npm, Yarn and pnpm (Node.js)

The `npm` ecosystem plugin handles Node.js projects managed by npm, Yarn
(classic and Berry) or pnpm, including workspaces.

## Detection

A directory is a project root if it has a lockfile (`package-lock.json`,
`npm-shrinkwrap.json`, `pnpm-lock.yaml`, `yarn.lock`), a
`pnpm-workspace.yaml`, or a `package.json` with `workspaces`. Workspace
members (matched by the `workspaces` patterns or `pnpm-workspace.yaml`
`packages`) belong to their root's project. A nested `package.json` that is
neither a member nor has its own lockfile, such as a test fixture, is
ignored. A `package.json` outside any root is its own project, unless it
declares no dependencies.

The package manager comes from the lockfile. Without one, Deepward uses the
`packageManager` field (Corepack), then `pnpm-workspace.yaml` (pnpm), and
defaults to npm.

## Dependency graph

The graph is read from the committed lockfile, so scans need neither Node.js
nor the package manager:

| Lockfile | Versions |
|----------|----------|
| `package-lock.json`, `npm-shrinkwrap.json` | lockfileVersion 1, 2, 3 |
| `yarn.lock` | Yarn classic (v1) and Berry (v2+) |
| `pnpm-lock.yaml` | lockfileVersion 5.x, 6, 9 |

Edges follow the package manager's resolution: nested `node_modules` for
npm, descriptors for Yarn, and package keys (including peer variants) for
pnpm. Workspace members and linked folders are part of the project, not
graph nodes. Their dependencies are direct dependencies of the project.
Packages reached only through `devDependencies` have scope `development`.
Everything else has scope `runtime`. Exclude development packages with:

```yaml
plugins:
  dependency-vulnerabilities:
    excludeScopes: [development]
```

Without a lockfile, the package manager generates one temporarily, without
installing packages or running scripts. Afterwards Deepward removes every
file the package manager created:

```text
npm install --package-lock-only --ignore-scripts --no-audit --no-fund
pnpm install --lockfile-only --ignore-scripts --no-frozen-lockfile
yarn install --mode=update-lockfile --no-immutable        # Yarn 2+
yarn install --ignore-scripts --modules-folder <tmp>       # Yarn classic
```

## Registries

Versions come from the npm registry protocol (abbreviated metadata).
Deprecated versions are never proposed. Configure scoped or private
registries in order. Without an unscoped entry, `https://registry.npmjs.org`
is used for everything else:

```yaml
plugins:
  npm:
    registries:
      - url: https://npm.pkg.github.com
        scope: "@acme"
        tokenEnv: GITHUB_TOKEN
      - url: https://nexus.acme.local/repository/npm-group
        usernameEnv: NEXUS_USER
        passwordEnv: NEXUS_PASSWORD
```

`.npmrc` and `.yarnrc.yml` are used by the package manager itself when fixes
are realized, so put the same registries there.

## Fixes

- **Direct dependencies.** The range is rewritten in every `package.json`
  that declares it, keeping its operator: `^0.21.0` becomes `^0.21.2`, an
  exact `1.2.5` becomes `1.2.6`, and `npm:alias@^1` keeps the alias. Ranges
  that cannot be rewritten safely (`*`, `latest`, `x` ranges, `||`, hyphen
  ranges, `>=`, `workspace:`, `file:`, git URLs) are reported as manual
  changes.
- **Transitive dependencies.** npm first tries a lockfile-only
  `npm update <name>`. If that leaves the vulnerable version, Deepward adds
  an override limited to it:

  | Package manager | Entry |
  |-----------------|-------|
  | npm | `"overrides": {"follow-redirects@1.14.7": "1.14.8"}` |
  | pnpm | `"pnpm": {"overrides": {"follow-redirects@1.14.7": "1.14.8"}}` |
  | Yarn | `"resolutions": {"follow-redirects": "1.14.8"}` |

  Yarn resolutions cannot target one version. If the package is also
  installed in another major version, or at a higher version, the change is
  manual.

The lockfile is then regenerated with the project's package manager
(lockfile-only, `--ignore-scripts`), and the change is verified by
rebuilding the graph. The package manager must therefore be available to
`deepward fix`. Its version comes from your toolchain (Corepack,
`tools.pnpm.path`, a container image, …), see [toolchain.md](../toolchain.md).
