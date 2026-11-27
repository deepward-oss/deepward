# Design

## Context

Titles are built in `internal/engine/fix.go` (`describe` and `titleText`): a `Deepward: ` prefix, an optional `[unverified] `, a text that depends on the request (one package, a `fix.groups` rule or package group, a `type`/`project` batch), ` in <dir>` and ` (not built)`. The commit subject in `publish.go` (`commitMessage`) is that title without `Deepward: `. Branch names already use `{placeholder}` patterns (`branch.go`: `placeholderRe`, `renderBranch`, `ValidateBranchTemplate`), and requests are found again by branch prefix and the body marker, never by title. Configuration is decoded strictly (`internal/strictyaml`), so unknown keys are already reported with their line.

## Goals / Non-Goals

**Goals:**
- One pattern per kind, rendered with plain string substitution, validated when the configuration is loaded.
- Default behavior byte-for-byte unchanged when `fix.title` is not set.

**Non-Goals:**
- Patterns for the request body, the commit body, labels, reviewers or assignees.
- Values from the environment (`{env:...}`), conditions, loops or a template language.
- Translating the default titles.

## Decisions

**Configuration shape.** `FixConfig.Title` is a struct with `Security`, `Pin`, `Update` and `Group` string fields (`yaml:"security"` ...). Strict decoding then rejects unknown kinds with a "did you mean" hint for free, and the JSON Schema is generated from the struct like every other key. Alternative: a `map[string]string`, rejected because unknown keys would need hand-written checks and the schema would lose the key names.

**Choosing the kind.** From the change set: exactly one item that is a package change → `pin` when that item is a pin of a transitive version, `security` when the request fixes vulnerabilities (an update merged into a security fix counts as `security`), otherwise `update`; more than one item → `group`. A request made only of text edits (code-issue fixes) has no package values, so it keeps the default title. `pin` without a pattern falls back to `security`; any kind without a pattern keeps the default title.

**Placeholder values.** Taken from the change set and its first item: `{package}` is the package name as the ecosystem writes it (`group:artifact`, `@scope/name`, module path), `{name}` its part after the last `:` or `/`, `{from}`/`{to}` the item versions, `{count}` the number of items, `{severity}` the highest severity of the request's vulnerability findings (empty when it has none), `{project}` the project directory (empty at the root), `{ecosystem}` the project ecosystem, `{group}` the group slug used in branch names (`lz4-java`, `jackson`, `security`), `{update}` the item's update type (empty when unknown). In a `group` request `{package}`, `{name}`, `{from}`, `{to}` and `{update}` are empty. Rendering reuses the branch placeholder syntax and regular expression; the known-placeholder set is separate from the branch one.

**Empty project.** When the project is at the root, the pattern is rewritten before substitution, so a value that happens to contain a preposition is never touched: `(` `{project}` `)` and `[` `{project}` `]` are dropped (with whitespace inside the brackets), then a preposition from a fixed list (`in`, `for`, `at`, case-insensitive) followed by whitespace and `{project}` is dropped with the whitespace before it, then any remaining `{project}`. After substitution repeated spaces are collapsed, spaces before `:` and `,` removed and the title trimmed. Alternative: optional segments in the syntax (for example `[ in {project}]`), rejected as the template-like syntax the user did not want.

**Markers and length.** The rendered text is cut first, then markers are added, so they always survive: the text is limited to 255 characters minus the length of the markers that apply, cutting on a rune boundary and ending with `…` when cut. Default titles go through the same limit.

**Commit subject.** `describe` records whether the title came from a pattern (a `ChangeSet` field excluded from JSON output, or the subject itself). `commitMessage` uses the full title in that case and keeps `TrimPrefix(title, "Deepward: ")` otherwise, so commits of users without patterns are unchanged.

**Validation.** `Config.Validate` checks each pattern: every placeholder must be in the known set (the error names `fix.title.<kind>` and the placeholder), and the pattern must render a non-empty title when the project is at the root and only `{project}` is empty, which rejects patterns such as `{project}` alone. A title that still renders empty at run time (not reachable with validated patterns) falls back to the default title.

## Risks / Trade-offs

- [The preposition list covers English only] → It is documented; brackets (`fix({project})`) or a pattern without a preposition work in any language.
- [A pattern change rewrites the titles of open requests on the next run] → Expected and the same as today when the default text changes; branches, and therefore the requests themselves, stay the same.
- [`{severity}` and `{update}` are empty for some kinds] → Documented per kind; collapsing whitespace keeps such titles tidy, and validation only rejects titles that can be entirely empty.
- [Conventional Commits in the commit subject also carry `[unverified] `] → Intentional: an unconfirmed fix must not look like a normal one in the history either.
