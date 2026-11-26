# Cargo (Rust)

The `cargo` ecosystem plugin handles Rust crates and workspaces.

## Detection

A `Cargo.toml` with a `[workspace]` table is a project root. Crates below it
belong to its project unless `workspace.exclude` lists them, as in Cargo. A
crate outside any workspace is its own project. The manifests are every
member's `Cargo.toml`, the root's `Cargo.lock` and `.cargo/config.toml`.

## Dependency graph

The graph is read from `Cargo.lock`, so scans need no Rust toolchain.
Workspace members, path dependencies and git dependencies are part of the
project. The registry crates they depend on are its direct dependencies.
When a lockfile contains several versions of a crate (for example `syn` 1
and 2), its dependency entries select the right one.

Dependency kinds come from the members' `Cargo.toml`:

| Declared in | Scope |
|-------------|-------|
| `[dependencies]`, `[target.*.dependencies]` | `compile` |
| `[build-dependencies]` | `build` |
| `[dev-dependencies]` | `development` |

A transitive crate gets the widest scope through which it is reachable.

Without a committed `Cargo.lock` (common for libraries), Deepward runs
`cargo generate-lockfile`, reads the result and deletes it again.

## Versions

Versions come from sparse registry indexes, crates.io
(`https://index.crates.io`) by default. Yanked versions are never proposed.
Other registries are asked first, in order, and the first one that knows a
crate answers:

```yaml
plugins:
  cargo:
    registries:
      - name: corp
        index: sparse+https://cargo.acme.local/api/v1/crates/
        tokenEnv: CARGO_REGISTRIES_CORP_TOKEN
```

## Fixes

- **Lockfile only.** If every requirement of the crate already allows the
  target version, only `Cargo.lock` changes:

  ```text
  cargo update -p regex@1.5.4 --precise 1.5.5
  ```

- **Requirement bump.** Otherwise the requirement is rewritten first, keeping
  its operator (`"1.5"` → `"1.6.1"`, `"=1.6.0"` → `"=1.6.1"`). This applies
  in the member's `Cargo.toml` or, for `workspace = true` entries, in
  `[workspace.dependencies]`. Requirements with several comparators or
  wildcards are manual.
- **Transitive crates** are pinned with `cargo update --precise`. If the
  crates that depend on it do not allow the target version, the change is
  manual, with Cargo's error.

Requirements: Cargo 1.64 or later, only for fixes and missing lockfiles.
