// Package app is the public entry point for building deepward binaries,
// including custom binaries that add third-party plugins (see docs/plugins.md).
package app

import (
	"context"
	"os"
	"os/signal"

	"github.com/deepward-oss/deepward/internal/buildinfo"
	"github.com/deepward-oss/deepward/internal/cli"
)

// Main runs the deepward CLI with the plugins registered in the default
// registry and returns the exit code.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	env := cli.ProcessEnv(args)
	env.Version, env.Commit, env.Date = buildinfo.Version, buildinfo.Commit, buildinfo.Date
	env.NewTools = cli.DefaultTools
	return cli.Run(ctx, env)
}
