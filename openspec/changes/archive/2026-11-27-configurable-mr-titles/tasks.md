# Tasks

## 1. Configuration and validation

- [x] 1.1 Add `TitleConfig` (`security`, `pin`, `update`, `group`) as `FixConfig.Title` in `internal/engine/config.go`, and verify with an engine test that a config with `fix.title.bugfix` fails strict decoding naming the unknown key
- [x] 1.2 Add the known title placeholders and validation in `Config.Validate` (unknown placeholder names `fix.title.<kind>` and the placeholder; a pattern that renders empty at the root, such as `{project}`, is rejected), and verify both with table tests in `internal/engine`
- [x] 1.3 Regenerate `schema/deepward.schema.json` with `go test ./internal/cli -run Schema -update` and verify `TestCommittedSchemaIsUpToDate` passes

## 2. Rendering titles

- [x] 2.1 Implement title rendering in `internal/engine` (new `title.go`): kind selection (`pin` → `security` fallback, `update`, `group`, text-only requests keep the default), placeholder values, empty-project rewrite (brackets, prepositions `in`, `for`, `at`), whitespace tidy-up; verify with unit tests for every spec scenario (Conventional Commits, pin fallback, group with `{count}`, `fix({project})` and `in {project}` at the root and in `services/api`, empty `{from}` in a group)
- [x] 2.2 Use the rendering in `describe` with markers added after the 255-character cut (`[unverified] ` in front, ` (not built)` at the end), keeping default titles unchanged; verify with plan tests that an unverified pin gets `[unverified] fix(deps): pin …`, that a long title stays within 255 characters with markers intact, and that existing title assertions in `fix_test.go` and `grouping_test.go` still pass without patterns
- [x] 2.3 Make `commitMessage` use the rendered title as the subject when it came from a pattern and keep the current subject otherwise; verify with a `Publish` test against `sdktest.FakeRepo` that the commit message starts with `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0` for a pattern and with `fix vulnerabilities: …` without one

## 3. Documentation

- [x] 3.1 Document `fix.title` in `docs/configuration.md` (kinds, placeholders per kind, empty project rule, markers, commit subject) with the examples from exploration (Conventional Commits, scope from the project, Russian, severity first, Dependabot style), and verify `go test ./test/docs` validates every new YAML block
- [x] 3.2 Mention title patterns in the titles bullet of `docs/publishing.md` and add a `fix.title` entry under `[Unreleased]` in `CHANGELOG.md`; verify by reading the rendered Markdown

## 4. Integration check

- [x] 4.1 Run `golangci-lint run ./...`, `go test ./...` and the coverage check (`go run ./internal/tools/covercheck -profile coverage.out`), and verify all pass
- [x] 4.2 Run `deepward fix` (dry run) on `testdata/projects/maven-multi` with a Conventional Commits `fix.title` and verify the printed titles follow the pattern

## Workflow follow-up

- Archive the change with `/opsx:archive` after review.
