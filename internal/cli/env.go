// Package cli implements the deepward command-line interface: commands,
// configuration layering, CI detection, output channels and exit codes.
package cli

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Exit codes (see the cli spec).
const (
	ExitOK      = 0
	ExitGate    = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// ToolsFactory builds the toolchain for a run.
type ToolsFactory func(ctx context.Context, cfg engine.Config, ws *sdk.Workspace, reg *sdk.Registry, cacheDir string, env Env) (sdk.Tools, error)

// Env is everything the CLI needs from the process, injectable for tests.
type Env struct {
	Args     []string
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	Environ  func() []string
	Cwd      string
	Registry *sdk.Registry
	Clock    sdk.Clock
	// StdoutIsTTY enables colors (unless NO_COLOR is set).
	StdoutIsTTY bool
	NewTools    ToolsFactory
	Version     string
	Commit      string
	Date        string
}

// ProcessEnv returns an Env for the current process.
func ProcessEnv(args []string) Env {
	cwd, _ := os.Getwd()
	return Env{
		Args: args, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Environ: os.Environ, Cwd: cwd,
		Registry: sdk.DefaultRegistry(), Clock: sdk.SystemClock{}, StdoutIsTTY: isTerminal(os.Stdout),
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// envName maps a flag name to its DEEPWARD_* variable.
func envName(flag string) string {
	return "DEEPWARD_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}
