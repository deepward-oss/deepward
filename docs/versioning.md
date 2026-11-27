# Versioning

Deepward follows [Semantic Versioning 2.0](https://semver.org/spec/v2.0.0.html).
Releases are Git tags `vMAJOR.MINOR.PATCH`, for example `v0.1.0`. Deepward
prints versions without the `v`: `deepward version` shows
`deepward 0.3.0 (commit …, built …, sdk 0.2.0)`. `deepward --version` prints
the same line.

## Before 1.0

Deepward is in its `0.x` series:

- a **minor** release (`0.2.0`) may contain breaking changes;
- a **patch** release (`0.1.1`) contains only fixes and never breaks
  anything listed below;
- only the latest minor release gets fixes.

Breaking changes are listed under "Changed" or "Removed" in the
[changelog](../CHANGELOG.md) with what to do. Where possible, the old
behavior keeps working with a deprecation warning for one minor release.

From `1.0.0` on, breaking changes need a new major version.

## What the version covers

These are the public interface. Breaking changes to them follow the rules
above:

- commands, flags and exit codes of the CLI ([cli.md](cli.md));
- keys and values of `deepward.yaml` ([configuration.md](configuration.md),
  [schema](../schema/deepward.schema.json));
- the JSON result: its fields can be added in any release, while removing
  or changing a field raises `schemaVersion`;
- the SARIF output and the `not-built` tag;
- branch names, labels and the metadata marker of change requests, so open
  requests keep being updated after an upgrade;
- the Go packages `pkg/sdk`, `pkg/sdk/...` and `pkg/app`.

Not covered: report layout and wording, log messages, the order of findings
with the same severity, Go packages under `internal/` and `plugins/`.

## Other version numbers

| Number | Where | Changes when |
|--------|-------|--------------|
| `schemaVersion` | JSON result | a field is removed or changes meaning |
| SDK version | `deepward version`, `sdk.SDKVersion` | the plugin contracts change; plugins built for another SDK version may not compile |
| plugin version | `deepward plugins list` | built-in plugins show the Deepward version; third-party plugins show their own |

## Releasing

Maintainers release from `main`:

1. Move the entries under "Unreleased" in `CHANGELOG.md` to a new
   `## [X.Y.Z] - YYYY-MM-DD` section and update the links at the bottom.
2. Commit, then tag and push:
   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```
3. The `release` workflow runs the tests, builds the binaries, archives,
   checksums, SBOMs and the container image with GoReleaser, publishes the
   GitHub release with the changelog section as notes.

Tags with a pre-release suffix (`v0.2.0-rc.1`) are published as
pre-releases and do not move the `latest` and `X.Y` image tags.
