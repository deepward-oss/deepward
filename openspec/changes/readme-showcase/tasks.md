# Tasks

## 1. Demo repository

- [x] 1.1 Prepare the demo repository contents locally (copies of `testdata/projects/npm-app`, `go-app` and `cargo-app` in `npm/`, `go/` and `cargo/`, a short README that links back to Deepward, and `.github/workflows/deepward.yml` with a `scan` job on push and pull request and a `fix` job on `workflow_dispatch` only, using `actions/setup-go` and a pinned release archive with checksum verification) and verify `deepward scan` and `deepward fix` run on the copy and report findings in all three projects
- [ ] 1.2 With the maintainer's explicit go-ahead, create the public `deepward-oss/deepward-demo` repository on GitHub, turn off Dependabot security and version updates, push the contents and protect `main`; verify the settings show Dependabot updates off, no `dependabot.yml` exists, and the `scan` run succeeds and uploads the HTML report artifact and SARIF
- [ ] 1.3 Dispatch the `fix` job once and verify it opens Deepward pull requests (at least one security fix) whose descriptions show the verified changes, and that no Dependabot pull requests exist; record the pull request and workflow run URLs for the README

## 2. Self-check in this repository

- [ ] 2.1 Add `deepward.yaml` at the repository root that excludes `testdata/`, `plugins/*/*/testdata`, `test/` and `examples/`; verify `deepward scan` lists only the root Go module as a project and `deepward fix` (dry run) proposes no change outside `go.mod` and `go.sum`
- [x] 2.2 Add `.github/workflows/deepward.yml`: `scan` on push to `main` and `develop` and on pull requests (`--fail-on high`, HTML report artifact, SARIF upload) and `fix --publish --target-branch develop` on a weekly schedule and `workflow_dispatch`, installing a pinned release archive with checksum verification and using `secrets.GITHUB_TOKEN` with only the documented permissions; verify the YAML is valid and permissions follow the recommended setup in `docs/publishing.md`
- [ ] 2.3 With the maintainer's explicit go-ahead, push `develop` to `origin` and run the workflow; verify `scan` succeeds, SARIF appears in code scanning, and a dispatched `fix` targets `develop` (or reports nothing to do)
- [x] 2.4 Document the self-check in the README Development section (what it scans, where requests go) and verify the description matches `deepward.yaml` and the workflow

## 3. Visual assets

- [ ] 3.1 Write `docs/assets/demo.tape` (vhs) that copies `testdata/projects/npm-app` to a temporary directory and runs `deepward scan --fail-on high` and `deepward fix` without `--publish`, and add a `make demo-gif` target that renders it; verify `make demo-gif` produces `docs/assets/demo.gif` of about 15 to 25 seconds and under about 2 MB, readable at 800 px width
- [x] 3.2 Capture `docs/assets/report.png` from the demo repository's HTML report artifact at a fixed viewport, cropped to the summary and first findings, and optimize it; verify the image is legible at README width and matches the current report layout
- [ ] 3.3 Capture `docs/assets/pull-request.png` from one demo security pull request (title, description with verification details, diff summary) in the light theme and optimize it; verify it shows the same pull request recorded in 1.3
- [x] 3.4 Mention `make demo-gif` and the vhs prerequisite in the Development section of the README draft, and verify the command listed there matches the Makefile target

## 4. Comparison research

- [x] 4.1 Check Dependabot, Renovate and Trivy against their current official documentation for each candidate row (fix checked before the request is opened, transitive fix by upgrade or pin, HTML/JSON/SARIF report, same tool locally and in any CI); record each cell as yes, no or partial with the source link, and verify every cell in the final table has a source and unclear cells are dropped
- [x] 4.2 Choose three or four rows where Deepward differs and draft the table with an "as of <month year>" line and source links; verify no row is one where all four tools are equal

## 5. README rewrite

- [ ] 5.1 Rewrite the hero: banner with alt text set to the tagline, tagline "Dependency fixes, verified before they reach your review.", two-sentence subtitle that defines "verified" as the targeted findings gone and the graph still resolving, and badges (release, `ci` workflow, self-check workflow, MIT, OSV.dev); verify the badges render on GitHub, the self-check badge links to the workflow from 2.2, and the hero, GIF and quickstart fit the first screen at a common laptop size
- [ ] 5.2 Add the demo GIF and the three-line quickstart (`docker run` scan for a lockfile project, `deepward fix`, `deepward fix --publish`) with a "see it live" link to the demo repository; verify each command runs as written against `testdata/projects/npm-app`
- [x] 5.3 Add the "Why Deepward" section with four pain-to-answer items about fixes and the short orchestrator paragraph after it; verify every statement maps to behavior described in `docs/publishing.md` or `docs/configuration.md` and links there
- [x] 5.4 Add the comparison table from 4.2; verify it renders correctly on GitHub and its links resolve
- [ ] 5.5 Add "Works with" (ecosystems, CI platforms, a minimal GitHub Actions snippet that includes installing Deepward, link to `docs/publishing.md` for GitLab and the full setup) and "What you get" (report and pull request screenshots linked to the live workflow run and pull request); verify the snippet is valid workflow YAML and every image path exists
- [x] 5.6 Move installation details, documentation, development and license below, update every version string to the current release and fix the `0.1` Dockerfile example; verify `grep` finds no stale version strings in `README.md`
- [x] 5.7 Review the whole README for the "verified" wording rule and for English-only content; verify no text claims or implies that fixes pass the project's tests or compile it, and that the file has no non-ASCII letters outside intentional symbols

## 6. Integration check

- [ ] 6.1 Preview the README on GitHub (a pushed branch or the rendered preview) in light and dark themes and on a narrow window; verify images, badges, table and links all load and the layout reads in order hero, GIF, quickstart, why, comparison, works with, what you get, install
- [ ] 6.2 Run `make test` and `make lint` and verify both pass, confirming the Makefile change and `deepward.yaml` did not break the build or the tests

## Workflow follow-up

- Archive the change after the README is released.
- Note the missing install step in the GitHub Actions example of `docs/publishing.md` as a separate follow-up change.
