// Package configpath resolves a tool from tools.<name>.path.
package configpath

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Resolver is the config-path tool resolver.
type Resolver struct{}

func init() { sdk.Register(Resolver{}) }

// Info implements sdk.Plugin.
func (Resolver) Info() sdk.Info {
	return sdk.Info{ID: "config-path", Kind: sdk.KindToolResolver, Version: sdk.BuiltinVersion,
		Description: "Uses the explicit path configured in tools.<name>.path"}
}

// Resolve implements sdk.ToolResolver.
func (Resolver) Resolve(_ context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	p := req.Settings.Path
	if p == "" {
		return nil, nil
	}
	if !filepath.IsAbs(p) && req.Workspace != nil {
		p = filepath.Join(req.Workspace.Root, filepath.FromSlash(p))
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("configured path %s: %w", p, err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("configured path %s is a directory", p)
	}
	return &sdk.ToolCandidate{Command: []string{p}, Source: p}, nil
}
