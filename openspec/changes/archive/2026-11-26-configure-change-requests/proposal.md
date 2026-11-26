## Why

Change requests are opened one per package, on branches named
`<prefix><ecosystem>/<group>`. Three problems came up while trying it on a
real monorepo:

- two projects of the same ecosystem with the same vulnerable package get the
  same branch, so the second commit overwrites the first;
- a package that is both vulnerable and outdated gets two requests that edit
  the same line, and merging one makes the other conflict;
- teams want to choose the branch naming and whether fixes come one request
  per package or in batches.

## What Changes

- **BREAKING** `fix.branchPrefix` is replaced by `fix.branch`, a branch name
  template with `{ecosystem}`, `{project}` and `{group}`. The default
  `deepward/{ecosystem}/{project}/{group}` adds the project directory for
  projects outside the repository root, so branches never collide.
  Templates that would still give two requests one branch stop `deepward fix`
  with an error.
- New `fix.grouping`: `package` (default, one request per package), `type`
  (per project, one request for security fixes and one for updates) or
  `project` (one request per project). `fix.groups` rules still take
  precedence. Batches get titles that count their changes.
- The update of a package is merged into the security fix of the same
  package: Deepward tries the update version first, and the request fixes and
  updates in one change. If that version fails verification, the security fix
  goes alone and the update is reported as failed.
- A request whose changes moved to another request is closed with a comment
  naming that request.

## Capabilities

### New Capabilities

### Modified Capabilities

- `change-requests`: branch template, grouping strategies, update merging,
  close comments for moved changes.

## Impact

- `internal/engine`: config (`fix.branch`, `fix.grouping`), grouping, fix
  loop, change-set description, publishing.
- `schema/deepward.schema.json`: regenerated; string fields can declare
  enums.
- Docs: `configuration.md`, `publishing.md`.
- Existing open requests of projects outside the root move to new branches:
  the old ones are closed on the next run.
