# Design

## Context

See proposal.md for motivation and scope. Current state that shapes the
approach:

- The repository is hosted on GitHub (`deepward-oss/deepward`); CI is
  `.github/workflows/ci.yml` (workflow name `ci`), releases come from
  `release.yml` with GoReleaser.
- `docs/assets/` already holds the banner (`deepward-banner.svg`), icons and
  a social card. The banner text says "Universal checks orchestrator for CI"
  and "verified fixes as merge and pull requests", and lists the ecosystems.
- `deepward scan` writes `deepward-out/deepward-report.html` and `.json` by
  default; `deepward fix` without `--publish` prints a unified diff per
  change request and writes nothing.
- `testdata/projects/npm-app` is a lockfile-based project with known
  vulnerable versions (`lodash 4.17.20`, `axios ^0.21.0`, `minimist 1.2.5`),
  so it scans with the static binary alone, without Maven, .NET or Go.
- GitHub publishing needs `DEEPWARD_GITHUB_TOKEN` with `contents: write`,
  `pull-requests: write`, `issues: write`; `secrets.GITHUB_TOKEN` is enough
  inside the target repository.
- `testdata/projects/go-app` needs the Go toolchain to resolve;
  `testdata/projects/cargo-app` is lockfile-based like `npm-app`.
- The repository itself is a Go module with seven direct dependencies and
  has no `deepward.yaml`. Scanning it as is would also pick up every fixture
  under `testdata/projects` (Maven, sbt, NuGet, npm, Yarn, pnpm, Go, Cargo),
  which carry intentionally vulnerable versions. `exclude:` in
  `deepward.yaml` removes directories from the scan; `--target-branch`
  overrides the detected default branch for `fix`.
- Work happens on `develop` and is released to `main`; `develop` exists
  locally but not yet on `origin`.
- Doc tests in `test/docs` cover `docs/*.md` only, not `README.md`.
- Hard-coded versions in the README (`0.4.0`, and a stale `0.1` in the
  Dockerfile example) are updated by hand at release time.

## Goals / Non-Goals

**Goals:**

- A visitor understands the problem, the differentiator and what the result
  looks like within the first screen, without scrolling past installation
  details.
- Every visual is reproducible from files in a repository, so it can be
  regenerated when the output changes.
- Every claim about Deepward and about other tools is accurate and checkable.

**Non-Goals:**

- No changes to the binary, configuration schema, CLI output or report
  layout to make screenshots look better.
- No GitHub Action or install script for Deepward; the README uses the
  existing release archives and image.
- No rewrite of `docs/*.md`. The missing install step in the GitHub Actions
  example of `docs/publishing.md` is noted but left for a separate change.
- No website, docs portal or translated README.
- The banner artwork is not redrawn.

## Decisions

### README structure and length

Order, top to bottom:

1. Banner (existing SVG, alt text updated to the tagline).
2. Tagline as the first heading line, then a two-sentence subtitle: what
   Deepward finds (vulnerable and outdated dependencies, transitive too, six
   ecosystem families) and what "verified" means (the targeted findings are
   gone and the dependency graph still resolves after the edit).
3. Badges: latest release, `ci` workflow status, "dependencies checked by
   Deepward" (the self-check workflow), MIT license, "data: OSV.dev".
   Shields.io for release, license and data, GitHub's own badges for the
   workflows.
4. Demo GIF.
5. Quickstart: one `docker run` line for a lockfile project, then
   `deepward fix` (dry run) and `deepward fix --publish`, three lines total,
   with a link to "see it live" in the demo repository.
6. "Why Deepward": four short pain-to-answer items, all about fixes.
7. Comparison table (see below).
8. "Works with": ecosystems and CI platforms in one compact block, with a
   minimal GitHub Actions snippet and a link to `docs/publishing.md` for
   GitLab and the full setup.
9. "What you get": the HTML report screenshot and the pull request
   screenshot, each with a one-line caption.
10. Installation (archives, checksums, image, `go install`), Documentation
    table, Development, License, unchanged in substance.

The "universal checks orchestrator" positioning stays, but moves from the
first sentence to a short paragraph after "Why", so the README does not
contradict the project context while leading with fixes.

Alternative considered: keep the current order and add visuals on top. It
keeps installation above the value proposition, which is the main problem.

### Tagline

"Dependency fixes, verified before they reach your review." It names the
differentiator, makes a claim Deepward can keep, and does not mention tests.
Alternatives considered: "Find vulnerable dependencies. Get pull requests
that actually build." (implies a full build and tests), and "One quality
gate for every check in your CI." (orchestrator positioning, rejected as the
hook).

### Wording rule for "verified"

Wherever the README says "verified", the nearby text or the linked section
explains it as "the targeted findings are gone and the graph still
resolves". Running tests appears only as an option through `fix.postTasks`.
No README text says or implies that fixes pass the project's tests or
compile it.

### Demo repository

A new public GitHub repository, `deepward-oss/deepward-demo`, seeded with
copies of `testdata/projects/npm-app`, `go-app` and `cargo-app` in
directories `npm/`, `go/` and `cargo/`. It is the showcase: it always has
findings and security fixes to show.

- GitHub rather than GitLab: the main repository and its visitors are on
  GitHub, and a pull request screenshot from the same platform is the most
  familiar proof.
- npm, Go and Cargo rather than Maven or .NET: three ecosystems show that one
  tool covers a polyglot repository, while CI stays light (the static binary
  plus `actions/setup-go`); their vulnerabilities (lodash, axios, minimist
  and the Go and Cargo fixtures' advisories) are real and recognizable.
- One workflow with two jobs, following the recommended setup in
  `docs/publishing.md`: `scan` on push and pull request (uploads the HTML
  report as a workflow artifact and SARIF to code scanning), and `fix` on
  `workflow_dispatch` only, with `secrets.GITHUB_TOKEN`. Deepward is
  installed from a pinned release archive with checksum verification.
- `fix` runs on demand, not on a schedule, so the showcased pull requests
  stay stable. The pull requests are left open and unmerged; the README
  links to them.
- Dependabot security updates and version updates are turned off in the
  repository settings, and no `dependabot.yml` is added, so the only
  automated requests are Deepward's. Dependabot alerts may stay on; they do
  not open requests.

Alternative considered: screenshots from a private or throwaway repository.
They cannot be linked as "see it live" and cannot be reproduced by others.
Alternative considered: leaving Dependabot on for a side-by-side comparison.
Rejected: it mixes two bots' requests on the showcase, and the comparison
belongs in the documented table, not in a staged repository.

### Self-check in this repository

Deepward checks its own dependencies, which shows it is used on a real
project, while the demo repository carries the showy findings.

- A `deepward.yaml` at the repository root excludes `testdata/`,
  `plugins/*/*/testdata`, `test/` and `examples/`, so only the root Go
  module is scanned and no request ever touches the intentionally vulnerable
  fixtures. The plugin fixtures need their own pattern: `exclude` matches a
  directory prefix or a single-level glob, and has no `**`.
- The same file sets `fix.transitive: pin`. The first self-check run fixed
  a vulnerable `golang.org/x/text` by upgrading `osv-scalibr`, which added
  more than 130 indirect modules and raised the `go` directive; pinning the
  transitive module keeps the change to one line.
- A new workflow `.github/workflows/deepward.yml`, separate from `ci`:
  `scan` on push to `main` and `develop` and on pull requests, with
  `--fail-on high`, the HTML report as an artifact and SARIF uploaded to
  code scanning; `fix --publish --target-branch develop` on a weekly
  schedule and on `workflow_dispatch`, with `secrets.GITHUB_TOKEN`.
- Deepward is installed in the workflow from the latest published release
  archive (pinned version, checksum verified), not built from the checked-out
  source: the badge then means "the released tool checks this repository".
- The README badge points at this workflow's status on `main`.
- Requests from the self-check are usually updates of outdated
  dependencies, not security fixes; that is expected and is why the
  screenshots come from the demo repository.

Alternative considered: self-check only, without a demo repository. The
repository likely has no vulnerable dependencies, so the report and pull
request would not show what Deepward does. Alternative considered: running
the self-check inside the `ci` workflow. A separate workflow keeps the write
token away from the build jobs and gives the badge its own status.

### Demo GIF

A vhs script, `docs/assets/demo.tape`, rendered to `docs/assets/demo.gif`.

- It runs the real binary against a temporary copy of
  `testdata/projects/npm-app`: `deepward scan --fail-on high` (summary
  table), then `deepward fix` (dry run with diffs and the verified/manual
  summary). No `--publish`, no tokens.
- Target: about 15 to 25 seconds, terminal width that stays readable at the
  README's rendered width, file size under about 2 MB.
- A `make demo-gif` target runs vhs. vhs is a local tool for maintainers
  only; it is not a build, test or CI dependency and is not compiled into
  the binary, so the dependency license rule does not apply to it.

Alternatives considered: asciinema (needs a player or an external host to
embed; GitHub does not render it inline), a hand-made screen recording (not
reproducible).

### Screenshots

- `docs/assets/report.png`: the HTML report from the demo repository's
  `scan` artifact, opened in a browser at a fixed viewport, cropped to the
  summary and the first findings.
- `docs/assets/pull-request.png`: a Deepward pull request in the demo
  repository (title, description with verification details, diff summary),
  light theme.
- Both are PNGs, optimized, each linked to the live page (the workflow run
  and the pull request) so the reader can check them.

### Comparison table

Columns: Deepward, Dependabot, Renovate, Trivy. Three or four rows chosen
from where Deepward differs, for example: "fix checked by re-resolving the
graph before the request is opened", "transitive fix by upgrade or pin",
"one report in HTML, JSON and SARIF", "same tool runs locally and in any
CI". Rows where all tools are equal (number of ecosystems, free to use) are
left out.

Each cell is checked against the tool's current official documentation
before the table is written; cells use "yes", "no" or "partial" with a
footnote where "partial" needs a sentence. The table carries an "as of
<month year>" line and links to the documentation pages used. Unclear cells
are dropped rather than guessed.

### Versions in the README

All version strings in install snippets use the current release, and the
Dockerfile example is corrected from `0.1`. The release process already
updates these by hand; this change does not automate it.

## Risks / Trade-offs

- [Competitor features change and the table becomes wrong] → "as of" date,
  links to sources, conservative wording, and only rows that are central to
  Deepward's design rather than to the other tools' roadmaps.
- [Screenshots and GIF drift from the real output after a release] → all
  three are reproducible (tape script, demo repository); regenerating them
  is a checklist item when the report layout or `fix` output changes.
- [The demo repository's pull requests get closed, merged or replaced] →
  `fix` runs only on manual dispatch; the README links to specific pull
  requests; branch protection on `main` of the demo repository.
- [Advisories for the demo dependencies change and the output differs] →
  acceptable: the demo shows real current data; the GIF is regenerated from
  the tape when needed.
- [The self-check opens requests against test fixtures] → `exclude` for
  `testdata/`, `test/`, `examples/`, verified with a dry-run `deepward fix`
  before the workflow gets a write token.
- [The self-check badge turns red after a new advisory] → that is the
  intended signal; the scheduled `fix` opens the request that clears it.
- [`develop` missing on `origin` makes `fix --target-branch develop` fail]
  → push `develop` before enabling the scheduled job.
- [Image-heavy README loads slowly] → one GIF under about 2 MB, two
  optimized PNGs, SVG banner.
- [Marketing tone erodes trust with engineers] → every claim stays checkable
  and links to the doc that backs it; no superlatives without a source.
- [README grows long] → hero, GIF and quickstart fit the first screen;
  reference material stays in `docs/`.

## Migration Plan

Documentation, assets, one configuration file and one workflow; no product
code. Order: the demo repository first, since the screenshots and "see it
live" links depend on it; the self-check next, since the badge depends on
it; then the GIF; then the README rewrite. Rollback is reverting the README,
assets, `deepward.yaml` and workflow commit; the demo repository can be
archived independently.

## Open Questions

- Final comparison rows, chosen after checking the other tools'
  documentation (does not change the table's shape or the tasks).
- Exact subtitle wording, settled while writing the README.
