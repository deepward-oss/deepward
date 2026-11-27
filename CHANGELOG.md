# Changelog

All notable changes to Deepward are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) as described in
[docs/versioning.md](docs/versioning.md).

## [Unreleased]

## [0.3.0] - 2026-11-27

### Added

- `fix.transitive`: `upgrade` (default) or `pin`, which change is tried
  first for a vulnerable transitive dependency; the other is tried when the
  first does not pass verification.
- `fix.postTasks`: commands run in the copy of the project after each change
  and before verification, like Renovate's `postUpgradeTasks`; their changes
  to the files matching `files` (globs with `**`) go into the change
  request, other changes are undone, and a failing task rejects the change.
- `fix.unverified.publish`: changes after which the targeted findings are
  still reported, but the project builds, get a merge/pull request titled
  `[unverified]`, labeled `deepward-unverified`, with a warning, the reason
  and a command to inspect the dependency tree.

### Changed

- The HTML report has a new, compact layout: fixed-width tables with
  identifiers in a monospace font that never wrap, the package namespace
  muted, short severity labels with one colored dot per advisory, dependency
  paths as chains that end in the highlighted vulnerable package, advisories
  as a small table inside each row with descriptions and references folded,
  a one-line summary, a sticky toolbar with section links, filters,
  expand/collapse all, and `/` to search.
- Rejected changes are logged as warnings (`change rejected`) with the
  reason, and the summary says how to publish them as unverified, so failed
  verifications no longer pass silently.

## [0.2.1] - 2026-11-26

### Added

- `fix.minSeverity` and `fix.kinds` choose which findings get merge/pull
  requests, for example only critical vulnerabilities; `deepward fix
  --min-severity` overrides the severity for one run. Open requests of the
  findings left out are kept as they are, not closed.
- Progress logs on stderr for `deepward scan` and `deepward fix`: projects,
  dependency graphs, analyzers and every change being verified, with
  durations. The new `log` section sets `level`, `format` and `progress`.
- `fix.concurrency` (default 2): change groups are verified in parallel.

### Changed

- `deepward fix` copies each project once per worker and reuses the copy for
  all of its change groups, instead of copying the project for every group.
- `--log-format` no longer defaults to `text` explicitly, so `log.format` in
  the configuration applies when the flag is not given.

## [0.2.0] - 2026-11-26

### Changed

- The HTML report shows one row per vulnerable package version, with all of
  its advisories inside, instead of one row per advisory. JSON and SARIF still
  carry one finding per advisory, so suppressions and the quality gate are
  unchanged.
- For a transitive vulnerability, the closest direct dependency is now the
  one recommended for an upgrade (it used to be the alphabetically first one).
- SDK 0.2.0: `deps.Graph.DirectAncestors` returns the closest ancestors
  first instead of sorting them by ID.

### Added

- Maven: the recommendation for a transitive vulnerability names the oldest
  version of the direct dependency whose published POM brings a fixed version
  (`ancestorUpgrade` in the finding properties), and `deepward fix` tries that
  version first. When no release brings a fix, or the direct dependency is not
  in any configured repository, the report says so and recommends a pin.
- SDK: `deps.DependencyLister`, optionally implemented by version registries,
  lists the dependencies a published package version declares.

### Removed

- Build provenance attestations of release archives and the container image:
  GitHub does not offer them for this repository on its current plan.
  Verify downloads with `checksums.txt`.

## [0.1.0] - 2026-11-26

First public release.

### Added

- `deepward scan`: dependency graphs from the native build tools and
  lockfiles of Maven, sbt, NuGet, npm, Yarn, pnpm, Go modules and Cargo,
  including transitive dependencies.
- Vulnerability analysis with data from OSV.dev (online and offline with
  `deepward db update`) and the GitHub Advisory Database. Data licenses are
  checked and credited in every report.
- Outdated dependency detection with patch, minor and major updates and
  per-package policies.
- Go modules that are resolved but not compiled for the target platforms are
  reported as "not built": real severity and fixes, shown apart, excluded from
  the quality gate.
- HTML, JSON and SARIF reports; CycloneDX SBOM output; `deepward report`
  renders reports from a saved JSON result.
- Quality gate (`failOn`), ignore rules with reasons and expiry dates.
- `deepward fix`: edits verified by rebuilding the graph, for direct and
  transitive dependencies (ancestor upgrades or pins), with lockfiles
  regenerated by the project's package manager.
- `deepward fix --publish`: GitLab merge requests and GitHub pull requests,
  one per package or batched per type or project (`fix.grouping`), on
  configurable branches (`fix.branch`), with limits, labels and pause on
  human commits. The update of a package is merged into its security fix.
- Toolchain with config path, wrapper, `PATH`, container and
  checksum-verified download resolvers; setup steps; config-only integrations
  of external checkers with SARIF import; `deepward doctor`.
- Public plugin SDK (`pkg/sdk`) and an entry point for custom binaries
  (`pkg/app`).
- Release binaries for Linux, macOS and Windows (amd64 and arm64), a
  container image at `ghcr.io/deepward-oss/deepward`, SBOMs and build
  provenance attestations.

[Unreleased]: https://github.com/deepward-oss/deepward/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/deepward-oss/deepward/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/deepward-oss/deepward/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/deepward-oss/deepward/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/deepward-oss/deepward/releases/tag/v0.1.0
