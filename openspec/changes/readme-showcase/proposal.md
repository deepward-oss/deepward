# Proposal

## Why

The README is accurate but reads like a specification: it opens with an
abstract category ("universal checks orchestrator"), shows no result, and
hides the strongest differentiator (fixes verified before a request is
opened) in the middle of a feature list. A visitor cannot see in a few
seconds what problem Deepward solves or why it beats the bots they already
use, so the README should become the project's landing page.

## What Changes

- **Hero built around verified fixes.** A one-line tagline (working draft:
  "Dependency fixes, verified before they reach your review.") and a short
  subtitle that states what "verified" means precisely: the targeted
  vulnerability is gone and the dependency graph still resolves after the
  edit. The README never claims that fixes pass the project's tests; running
  tests is described only as an option through `fix.postTasks`.
- **Badges** under the hero: release, CI, license, vulnerability data
  source.
- **Visual proof**, all reproducible:
  - an animated terminal recording (vhs `.tape` script kept in the repo,
    rendered to a GIF in `docs/assets/`) showing `scan` and `fix` with a
    summary of verified and manual changes;
  - a screenshot of the HTML report;
  - a screenshot of a real merge/pull request opened by Deepward.
- **Public demo repository** `deepward-oss/deepward-demo` with copies of
  the `npm-app`, `go-app` and `cargo-app` fixtures, whose CI runs
  `deepward scan` and `deepward fix --publish`. It produces the pull request
  and report shown in the screenshots and is linked from the README as a
  live example. Dependabot is turned off there so only Deepward's requests
  appear.
- **Self-check**: Deepward scans its own repository in CI (SARIF to code
  scanning, scheduled `fix --publish` into `develop`), with the test
  fixtures excluded, and the README shows a "dependencies checked by
  Deepward" badge linked to that workflow.
- **One-line quickstart** (`docker run ... scan`) right after the visuals,
  before any installation details.
- **"Why Deepward" section**: four pain-to-answer items centered on fixes
  (bump that leaves a transitive vulnerability, a change that breaks
  resolution, noisy diffs, unexplained failures as `manual` /
  `[unverified]`).
- **Comparison table** against Dependabot, Renovate and Trivy: three or four
  rows where Deepward actually differs, every cell checked against those
  tools' current documentation, with an "as of" date. Snyk is out of scope.
- **Restructured order**: hero, GIF, quickstart, why, comparison,
  ecosystems and CI snippets, report and pull request screenshots, then
  install details, documentation, development and license. Detailed
  installation (checksums, `go install`) and development move lower.
- **Fix**: the Dockerfile example uses image tag `0.1`; it must match the
  current release.
- All content stays in English.

## Capabilities

### New Capabilities

None. This change edits documentation and adds demo assets; no product
behavior changes, so the change opts out of specs (`skip_specs: true`).

### Modified Capabilities

None.

## Impact

- `README.md` (rewritten; the uncommitted banner edit in the working copy is
  folded in).
- `docs/assets/`: new GIF, `.tape` script, report and pull request
  screenshots, alongside the existing banner and icons.
- Possibly a `make` target to re-render the GIF (needs vhs locally; not a
  build or CI dependency).
- A new public GitHub repository, `deepward-oss/deepward-demo`.
- This repository: a new `deepward.yaml` (scan scope and fix settings) and a
  new GitHub Actions workflow for the self-check; Deepward pull requests
  start appearing against `develop`, which must exist on `origin`.
- No code, configuration schema, CLI or binary dependency changes. README is
  not covered by the doc tests in `test/docs`.

## Open Questions

- Final tagline wording.
- Exact comparison rows, decided after checking the competitors'
  documentation.
