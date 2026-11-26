## 1. Branches

- [x] 1.1 Replace `fix.branchPrefix` with the `fix.branch` template; validate placeholders, `{group}` and the fixed prefix
- [x] 1.2 Render branches with the project directory slug; list requests by the template prefix
- [x] 1.3 Fail planning when two change sets get the same branch

## 2. Grouping

- [x] 2.1 Add `fix.grouping` (`package`, `type`, `project`) after `fix.groups` rules
- [x] 2.2 Count-based titles for batches; keep rule and per-package titles
- [x] 2.3 Merge an update into the security target of the same package and version; fall back and report the update when it fails

## 3. Publishing

- [x] 3.1 Table type "security fix, <type> update" for merged changes
- [x] 3.2 Close comment naming the requests that now carry the changes

## 4. Docs and verification

- [x] 4.1 Regenerate the JSON Schema (enum for `fix.grouping`); document `fix.branch`, `fix.grouping` and merging
- [x] 4.2 Unit tests, coverage, lint, integration tests
