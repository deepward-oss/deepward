// Package wrapper resolves project-local wrapper scripts such as mvnw.
package wrapper

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Resolver is the wrapper tool resolver.
type Resolver struct{}

func init() { sdk.Register(Resolver{}) }

// Info implements sdk.Plugin.
func (Resolver) Info() sdk.Info {
	return sdk.Info{ID: "wrapper", Kind: sdk.KindToolResolver, Version: sdk.BuiltinVersion,
		Description: "Uses project-local wrappers (mvnw, sbt launcher scripts) found in the project or its parents"}
}

// Resolve implements sdk.ToolResolver. It searches the project directory and
// its parents up to the workspace root.
func (Resolver) Resolve(_ context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	if len(req.Definition.Wrappers) == 0 || req.Workspace == nil {
		return nil, nil
	}
	root := filepath.Clean(req.Workspace.Root)
	dir := root
	if req.Project != nil {
		dir = filepath.Join(root, filepath.FromSlash(req.Project.Dir))
	}
	for {
		for _, name := range ordered(req.Definition.Wrappers) {
			p := filepath.Join(dir, name)
			fi, err := os.Stat(p)
			if err != nil || fi.IsDir() {
				continue
			}
			cmd := []string{p}
			if runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
				// Checked-out wrappers sometimes lose the executable bit.
				cmd = []string{"sh", p}
			}
			return &sdk.ToolCandidate{Command: cmd, Source: p}, nil
		}
		if dir == root || !strings.HasPrefix(dir, root) {
			return nil, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// ordered prefers .cmd/.bat wrappers on Windows and extensionless ones elsewhere.
func ordered(names []string) []string {
	var win, other []string
	for _, n := range names {
		ext := strings.ToLower(filepath.Ext(n))
		if ext == ".cmd" || ext == ".bat" {
			win = append(win, n)
		} else {
			other = append(other, n)
		}
	}
	if runtime.GOOS == "windows" {
		return append(win, other...)
	}
	return other
}
