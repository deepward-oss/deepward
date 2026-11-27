# Proposal

## Why

Merge/pull request titles are hard-coded ("Deepward: fix vulnerabilities: lz4-java 1.11.2 → 1.11.4"), so teams that follow Conventional Commits, write in another language or keep their own wording cannot make Deepward's requests fit their repository. Branch names are already configurable with simple placeholders; titles should be configurable the same way, without a template language.

## What Changes

- New `fix.title` section with one title pattern per kind of change request:
  - `security`: a vulnerability fixed by one package change;
  - `pin`: a vulnerability fixed by pinning a transitive version (falls back to `security`);
  - `update`: an update of one outdated dependency;
  - `group`: a request with several package changes (`fix.groups` rules, `fix.grouping: type` or `project`).
  A kind without a pattern keeps today's title.
- Patterns use `{placeholder}` syntax like `fix.branch`: `{package}`, `{name}`, `{from}`, `{to}`, `{count}`, `{severity}`, `{project}`, `{ecosystem}`, `{group}`, `{update}`. No conditions or loops.
- An empty `{project}` (a project at the repository root) is removed together with empty brackets around it and a short preposition before it, so `fix({project}): …` becomes `fix: …` and `… in {project}` loses ` in`.
- Deepward still marks unverified requests (`[unverified]` in front) and requests for packages that are not built (` (not built)` at the end) whatever the pattern says.
- With a custom pattern, the commit subject is the rendered title, so Conventional Commits reach the git history too. Without one, the commit subject stays as today.
- Unknown keys or placeholders are configuration errors that name the key; rendered titles are cut to 255 characters; a pattern that renders an empty title is rejected.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `change-requests`: change request titles and commit subjects come from configurable patterns per kind of request, with Deepward's default titles when no pattern is set.

## Impact

- `internal/engine`: `FixConfig` gains `Title`; title rendering in `describe`/`titleText` and the commit subject in `commitMessage` (`publish.go`) use the patterns; configuration validation covers them.
- `schema/deepward.schema.json` (regenerated), `docs/configuration.md` and `docs/publishing.md` (examples: Conventional Commits, Russian, severity first, Dependabot style), `CHANGELOG.md`.
- Existing requests keep their branch (branches do not depend on titles), so changing a pattern updates the titles of open requests on the next run instead of opening new ones.
- No new dependencies; no change to `pkg/sdk`.
