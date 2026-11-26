package cargo

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/cargo/domain"
)

// tailError adds the last lines of a failed tool's output to its error.
func tailError(err error) error {
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

type reqEdit struct {
	file string
	span domain.Span
	req  string
}

// requirementEdits returns the Cargo.toml edits that make every requirement
// of a crate allow the target version (none when they already do).
func requirementEdits(root string, project sdk.Project, manifests []*domain.Manifest, crate, to string) ([]reqEdit, *sdk.ManualChange, error) {
	var rootManifest *domain.Manifest
	for _, m := range manifests {
		if path.Dir(m.Path) == project.Dir {
			rootManifest = m
		}
	}
	var out []reqEdit
	declared := false
	check := func(file, key, req string) (*sdk.ManualChange, error) {
		declared = true
		if req == "" {
			return nil, nil
		}
		ok, err := domain.Satisfies(req, to)
		if err != nil || ok {
			return nil, nil
		}
		nr, err := domain.RewriteReq(req, to)
		if err != nil {
			return &sdk.ManualChange{Reason: fmt.Sprintf("%s in %s: %v", crate, file, err),
				Suggestion: fmt.Sprintf("allow %s %s in %s", crate, to, file)}, nil
		}
		data, err := deps.ReadFile(root, file)
		if err != nil {
			return nil, err
		}
		for _, s := range domain.FindRequirements(data, key) {
			if s.Value == req {
				out = append(out, reqEdit{file: file, span: s, req: nr})
			}
		}
		return nil, nil
	}
	for _, m := range manifests {
		for _, d := range m.Declarations(crate) {
			var manual *sdk.ManualChange
			var err error
			switch {
			case d.Workspace && rootManifest != nil:
				if wd, ok := rootManifest.WorkspaceDeps[d.Key]; ok {
					manual, err = check(rootManifest.Path, d.Key, wd.Req)
				}
			case !d.Path && !d.Git:
				manual, err = check(m.Path, d.Key, d.Req)
			}
			if err != nil || manual != nil {
				return nil, manual, err
			}
		}
	}
	if rootManifest != nil {
		for key, wd := range rootManifest.WorkspaceDeps {
			if wd.Package == crate && !declared {
				if manual, err := check(rootManifest.Path, key, wd.Req); err != nil || manual != nil {
					return nil, manual, err
				}
			}
		}
	}
	if !declared {
		return nil, &sdk.ManualChange{Reason: crate + " is not declared in Cargo.toml"}, nil
	}
	return dedupe(out), nil, nil
}

func dedupe(in []reqEdit) []reqEdit {
	seen := map[string]bool{}
	var out []reqEdit
	for _, e := range in {
		k := fmt.Sprintf("%s:%d", e.file, e.span.Start)
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// Realize implements sdk.RemediationRealizer.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	project := req.Project
	root := req.Workspace.Root
	manifests, err := projectManifests(req.Workspace.FS, project)
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	lockfile := project.Attributes["lockfile"]
	files := make([]string, 0, len(manifests)+1)
	for _, m := range manifests {
		files = append(files, m.Path)
	}
	if lockfile != "" {
		files = append(files, lockfile)
	}
	dir := filepath.Join(root, filepath.FromSlash(project.Dir))
	var res sdk.RealizeResult
	edits, err := deps.Regenerate(ctx, root, files, func(ctx context.Context) error {
		for _, c := range req.Changes {
			pu, err := purl.Parse(c.PURL)
			if err != nil {
				res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
				continue
			}
			crate := pu.Name
			if !c.Pin {
				edits, manual, err := requirementEdits(root, project, manifests, crate, c.To)
				if err != nil {
					return err
				}
				if manual != nil {
					manual.Change = c
					res.Manual = append(res.Manual, *manual)
					continue
				}
				if err := applyReqEdits(root, edits); err != nil {
					return err
				}
				if lockfile == "" {
					if len(edits) == 0 {
						res.Manual = append(res.Manual, sdk.ManualChange{Change: c,
							Reason: "Cargo.lock is not committed and the requirement already allows " + c.To})
					}
					continue
				}
			} else if lockfile == "" {
				res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "Cargo.lock is not committed, so a transitive crate cannot be pinned"})
				continue
			}
			if p.host == nil {
				return errors.New("cargo plugin not initialized")
			}
			tool, err := p.host.Tools().Require(ctx, cargoRequirement(), &project)
			if err != nil {
				return err
			}
			spec := crate
			if c.From != "" {
				spec += "@" + c.From
			}
			args := []string{"update", "-p", spec, "--precise", c.To}
			if _, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir, Env: cargoEnv}); err != nil {
				if c.Pin {
					res.Manual = append(res.Manual, sdk.ManualChange{Change: c,
						Reason:     fmt.Sprintf("the requirements of crates depending on %s do not allow %s: %v", crate, c.To, tailError(err)),
						Suggestion: fmt.Sprintf("upgrade the crates that depend on %s, then run cargo %s", crate, strings.Join(args, " "))})
					continue
				}
				return fmt.Errorf("cargo %s: %w", strings.Join(args, " "), tailError(err))
			}
		}
		return nil
	})
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	res.Edits = edits
	return res, nil
}

// applyReqEdits rewrites requirement strings in place.
func applyReqEdits(root string, edits []reqEdit) error {
	byFile := map[string][]reqEdit{}
	for _, e := range edits {
		byFile[e.file] = append(byFile[e.file], e)
	}
	for file, es := range byFile {
		data, err := deps.ReadFile(root, file)
		if err != nil {
			return err
		}
		// Apply from the end so earlier offsets stay valid.
		sort.Slice(es, func(i, j int) bool { return es[i].span.Start > es[j].span.Start })
		for _, e := range es {
			data = append(data[:e.span.Start], append([]byte(e.req), data[e.span.End:]...)...)
		}
		if err := deps.WriteFile(root, file, data); err != nil {
			return err
		}
	}
	return nil
}
