package npm

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/npm/domain"
)

// toolError adds the last lines of a failed tool's output to its error.
func toolError(err error) error {
	var xe *sdk.ExitError
	if errors.As(err, &xe) && len(xe.Tail) > 0 {
		tail := xe.Tail
		if len(tail) > 10 {
			tail = tail[len(tail)-10:]
		}
		return fmt.Errorf("%w:\n%s", err, strings.Join(tail, "\n"))
	}
	return err
}

// Realize implements sdk.RemediationRealizer. Direct updates rewrite the
// range in package.json, transitive pins add a version-limited override, and
// the lockfile is regenerated with the project's package manager.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	project := req.Project
	manifests, err := projectManifests(req.Workspace.FS, project)
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	manager := domain.Manager(project.Attributes["packageManager"])
	lockfile := project.Attributes["lockfile"]
	root := req.Workspace.Root
	files := make([]string, 0, len(manifests)+1)
	for _, dir := range domain.SortedKeys(manifests) {
		files = append(files, manifests[dir].Path)
	}
	if lockfile != "" {
		files = append(files, lockfile)
	}
	projectDir := filepath.Join(root, filepath.FromSlash(project.Dir))
	var res sdk.RealizeResult
	var tool sdk.Tool
	requireTool := func(ctx context.Context) (sdk.Tool, error) {
		if tool != nil {
			return tool, nil
		}
		if p.host == nil {
			return nil, errors.New("npm plugin not initialized")
		}
		t, err := p.host.Tools().Require(ctx, managerRequirement(manager), &project)
		if err != nil {
			return nil, err
		}
		tool = t
		return t, nil
	}
	edits, err := deps.Regenerate(ctx, root, files, func(ctx context.Context) error {
		applied := 0
		for _, c := range req.Changes {
			pu, err := purl.Parse(c.PURL)
			if err != nil {
				res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
				continue
			}
			name := deps.PackageName(pu)
			var manual *sdk.ManualChange
			if c.Pin {
				manual, err = p.pin(ctx, root, projectDir, manifests["."].Path, lockfile, manager, name, c, requireTool)
			} else {
				manual, err = bumpRange(root, manifests, name, c)
			}
			if err != nil {
				return err
			}
			if manual != nil {
				res.Manual = append(res.Manual, *manual)
				continue
			}
			applied++
		}
		if applied == 0 || lockfile == "" {
			return nil
		}
		t, err := requireTool(ctx)
		if err != nil {
			return err
		}
		return toolError(runLock(ctx, t, manager, projectDir))
	})
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	res.Edits = edits
	return res, nil
}

// bumpRange rewrites the range of a direct dependency in every package.json
// of the project that declares it.
func bumpRange(root string, manifests map[string]*domain.Manifest, name string, c sdk.PackageChange) (*sdk.ManualChange, error) {
	type change struct {
		file string
		data []byte
	}
	var changes []change
	for _, dir := range domain.SortedKeys(manifests) {
		m := manifests[dir]
		data, err := deps.ReadFile(root, m.Path)
		if err != nil {
			return nil, err
		}
		var edits []domain.JSONEdit
		for _, kind := range m.Declared(name) {
			start, end, old, ok := domain.StringValue(data, string(kind), name)
			if !ok {
				continue
			}
			nr, err := domain.RewriteRange(old, c.To)
			if err != nil {
				return &sdk.ManualChange{Change: c, Reason: fmt.Sprintf("%s in %s: %v", name, m.Path, err),
					Suggestion: fmt.Sprintf("set %q to a range that allows %s in %s", name, c.To, m.Path)}, nil
			}
			edits = append(edits, domain.JSONEdit{Start: start, End: end, Text: quote(nr)})
		}
		if len(edits) > 0 {
			changes = append(changes, change{file: m.Path, data: domain.ApplyJSONEdits(data, edits)})
		}
	}
	if len(changes) == 0 {
		return &sdk.ManualChange{Change: c, Reason: name + " is not declared in dependencies, devDependencies or optionalDependencies"}, nil
	}
	for _, ch := range changes {
		if err := deps.WriteFile(root, ch.file, ch.data); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// pin forces a transitive package to a fixed version.
func (p *Plugin) pin(ctx context.Context, root, projectDir, rootManifest, lockfile string, manager domain.Manager, name string, c sdk.PackageChange,
	requireTool func(context.Context) (sdk.Tool, error)) (*sdk.ManualChange, error) {
	if lockfile == "" {
		return &sdk.ManualChange{Change: c, Reason: "the project has no lockfile, so a transitive package cannot be pinned"}, nil
	}
	var objPath []string
	key := name + "@" + c.From
	switch manager {
	case domain.NPM:
		// Try a lockfile-only update within the declared ranges first.
		t, err := requireTool(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := t.Run(ctx, sdk.RunSpec{Args: []string{"update", name, "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"}, Dir: projectDir}); err != nil {
			return nil, fmt.Errorf("npm update %s: %w", name, toolError(err))
		}
		if gone, err := versionGone(root, lockfile, name, c.From); err != nil || gone {
			return nil, err
		}
		objPath = []string{"overrides"}
	case domain.PNPM:
		objPath = []string{"pnpm", "overrides"}
	case domain.Yarn:
		data, err := deps.ReadFile(root, lockfile)
		if err != nil {
			return nil, err
		}
		tree, err := domain.ParseYarnLock(data, nil)
		if err != nil {
			return nil, err
		}
		to, err := deps.ParseVersion(deps.NPM, c.To)
		if err != nil {
			return nil, err
		}
		toMajor, _, _, _ := to.Segments()
		for _, v := range tree.Instances(name) {
			pv, err := deps.ParseVersion(deps.NPM, v)
			if err != nil {
				continue
			}
			if major, _, _, _ := pv.Segments(); major != toMajor || to.Less(pv) {
				return &sdk.ManualChange{Change: c, Reason: fmt.Sprintf("yarn resolutions cannot target one version, and %s is also installed at %s", name, v),
					Suggestion: fmt.Sprintf(`"resolutions": {"**/<parent>/%s": "%s"}`, name, c.To)}, nil
			}
		}
		objPath, key = []string{"resolutions"}, name
	default:
		return &sdk.ManualChange{Change: c, Reason: "unknown package manager " + string(manager)}, nil
	}
	data, err := deps.ReadFile(root, rootManifest)
	if err != nil {
		return nil, err
	}
	edit, err := domain.SetMember(data, objPath, key, c.To)
	if err != nil {
		return &sdk.ManualChange{Change: c, Reason: fmt.Sprintf("%s: %v", rootManifest, err)}, nil
	}
	return nil, deps.WriteFile(root, rootManifest, domain.ApplyJSONEdits(data, []domain.JSONEdit{edit}))
}

// versionGone reports whether no copy of name@version is left in the lockfile.
func versionGone(root, lockfile, name, version string) (bool, error) {
	data, err := deps.ReadFile(root, lockfile)
	if err != nil {
		return false, err
	}
	tree, err := parseLock(path.Base(lockfile), data, nil)
	if err != nil {
		return false, err
	}
	for _, v := range tree.Instances(name) {
		if v == version {
			return false, nil
		}
	}
	return true, nil
}
