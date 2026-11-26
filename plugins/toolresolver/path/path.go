// Package path resolves tools from the PATH environment variable.
package path

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Resolver is the PATH tool resolver.
type Resolver struct {
	// Getenv overrides os.Getenv (for tests).
	Getenv func(string) string
}

func init() { sdk.Register(&Resolver{}) }

// Info implements sdk.Plugin.
func (*Resolver) Info() sdk.Info {
	return sdk.Info{ID: "path", Kind: sdk.KindToolResolver, Version: sdk.BuiltinVersion, Description: "Finds tools on PATH"}
}

// Resolve implements sdk.ToolResolver.
func (r *Resolver) Resolve(_ context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, dir := range filepath.SplitList(getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, name := range req.Definition.Executables {
			p := filepath.Join(dir, name)
			fi, err := os.Stat(p)
			if err != nil || fi.IsDir() {
				continue
			}
			if runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
				continue
			}
			return &sdk.ToolCandidate{Command: []string{p}, Source: p}, nil
		}
	}
	return nil, nil
}
