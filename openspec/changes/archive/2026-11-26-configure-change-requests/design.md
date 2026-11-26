## Context

The fix pipeline groups findings into targets (findings sharing the same
remediation alternatives) and groups targets into change sets, one per
project and group name. Each change set is verified on its own scratch copy
and becomes one merge or pull request on a branch derived from its group.

## Goals / Non-Goals

**Goals:**

- branches that never collide, with a configurable name;
- a choice between one request per package and batches;
- no two requests editing the same dependency line.

**Non-Goals:**

- merging an update of a direct dependency into the security fix of one of
  its transitive dependencies (the security fix may still upgrade the parent
  to a lower version than the update proposes);
- schedules or per-ecosystem grouping.

## Decisions

### D1: Template instead of prefix

`fix.branch` is a template with `{ecosystem}`, `{project}` (the slugged
project directory, empty at the root) and `{group}` (the slugged group name).
Empty placeholders collapse the `/` next to them, so root projects keep the
old branch names. The fixed text before the first placeholder is the prefix
used to list Deepward's requests, so the template must start with it.
Collisions are checked after planning instead of being prevented by
mandatory placeholders: a single-project repository can use `bot/{group}`.

Alternative: keep `branchPrefix` and always append the directory. Rejected:
it does not let teams match their own branch conventions.

### D2: Grouping strategies map findings to built-in group names

`package` keeps the remediation's own group (`security/<pkg>`,
`updates/<pkg>`). `type` maps vulnerabilities to `security`, updates to
`updates` and other findings to their analyzer. `project` maps everything to
`all`. Rules in `fix.groups` are matched first. Each target is still applied
and verified separately, so one failing package does not block the batch.

### D3: Updates join security fixes as a preferred alternative

After grouping, an update target whose change starts from the same package
and version as a non-pin alternative of a security target is moved into that
target as `upgrade`. The upgrade is tried first and must clear both the
vulnerability and the update findings; otherwise the security alternatives
run and the update is reported under its original group. Verification stays
the only judge of whether the newer version is acceptable.

### D4: Close comments name the new home

Before closing an open request that is no longer planned, Deepward checks
whether every `package@version` in its marker is part of a planned request of
the same project and, if so, names that request in the comment.

## Risks / Trade-offs

- [Open requests of non-root projects get new branches] → they are closed
  with a comment on the next run; the project is unreleased, so no migration
  path is kept for `branchPrefix`.
- [Large `project` batches take longer to verify] → each target is verified
  separately as before; the time is the same as for separate requests.
