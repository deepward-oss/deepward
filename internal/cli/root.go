package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/internal/integrations"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// exitError carries an explicit exit code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

func exitCode(code int, err error) error { return &exitError{code: code, err: err} }

// globalOpts are flags shared by every command.
type globalOpts struct {
	config    string
	logFormat string
	quiet     bool
	noColor   bool
}

// Run executes the CLI and returns the process exit code.
func Run(ctx context.Context, env Env) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Registry == nil {
		env.Registry = sdk.NewRegistry()
	}
	if env.Clock == nil {
		env.Clock = sdk.SystemClock{}
	}
	if env.Cwd == "" {
		env.Cwd, _ = os.Getwd()
	}
	g := &globalOpts{}
	ran := false
	root := &cobra.Command{
		Use:           "deepward",
		Short:         "Deepward is a universal checks orchestrator for CI",
		Long:          "Deepward finds vulnerable and outdated dependencies (Maven, sbt, NuGet, npm, Yarn, pnpm, Go, Cargo), produces HTML/JSON/SARIF reports,\noptionally opens merge/pull requests, and integrates external checkers through configuration.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			ran = true
			return applyEnv(cmd.Flags(), env)
		},
	}
	root.Version = versionLine(env)
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetArgs(env.Args)
	root.SetContext(ctx)
	root.CompletionOptions.DisableDefaultCmd = true
	pf := root.PersistentFlags()
	pf.StringVar(&g.config, "config", "", "path of the configuration file (default: <root>/deepward.yaml)")
	pf.StringVar(&g.logFormat, "log-format", "text", "log format on stderr: text or json")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "suppress the summary on stdout")
	pf.BoolVar(&g.noColor, "no-color", false, "disable colors (also NO_COLOR)")

	root.AddCommand(
		newScanCmd(env, g),
		newReportCmd(env, g),
		newFixCmd(env, g),
		newDBCmd(env, g),
		newPluginsCmd(env, g),
		newDoctorCmd(env, g),
		newConfigCmd(env, g),
		newVersionCmd(env),
	)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var xe *exitError
	if errors.As(err, &xe) {
		if xe.err != nil {
			fmt.Fprintln(env.Stderr, "deepward:", xe.err)
		}
		return xe.code
	}
	fmt.Fprintln(env.Stderr, "deepward:", err)
	if !ran || isUsage(err) {
		if !ran {
			fmt.Fprintln(env.Stderr, "Run 'deepward --help' for usage.")
		}
		return ExitUsage
	}
	return ExitRuntime
}

// applyEnv sets every flag that was not given explicitly from its
// DEEPWARD_* environment variable.
func applyEnv(fs *pflag.FlagSet, env Env) error {
	var errs []error
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			return
		}
		v := env.getenv(envName(f.Name))
		if v == "" {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			if err := sv.Replace(splitList(v)); err != nil {
				errs = append(errs, usagef("%s: %w", envName(f.Name), err))
			}
			f.Changed = true
			return
		}
		if err := f.Value.Set(v); err != nil {
			errs = append(errs, usagef("%s: %w", envName(f.Name), err))
			return
		}
		f.Changed = true
	})
	return errors.Join(errs...)
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func newLogger(env Env, g *globalOpts) (*slog.Logger, error) {
	level := slog.LevelInfo
	if g.quiet {
		level = slog.LevelWarn
	}
	opts := &slog.HandlerOptions{Level: level}
	switch g.logFormat {
	case "json":
		return slog.New(slog.NewJSONHandler(env.Stderr, opts)), nil
	case "text", "":
		return slog.New(slog.NewTextHandler(env.Stderr, opts)), nil
	}
	return nil, usagef("--log-format must be text or json, got %q", g.logFormat)
}

// colorsEnabled reports whether ANSI colors may be used on stdout.
func colorsEnabled(env Env, g *globalOpts) bool {
	return env.StdoutIsTTY && !g.noColor && env.getenv("NO_COLOR") == ""
}

// runFlags are shared by commands that run plugins.
type runFlags struct {
	offline      bool
	noCache      bool
	cacheDir     string
	allowPartial bool
	failOn       string
	formats      []string
	out          string
	sboms        []string
	sarifs       []string
	repoURL      string
	commit       string
	branch       string
	target       string
}

func (r *runFlags) register(cmd *cobra.Command, scan bool) {
	f := cmd.Flags()
	f.BoolVar(&r.offline, "offline", false, "do not access the network for vulnerability data (requires a local database)")
	f.BoolVar(&r.noCache, "no-cache", false, "disable the registry metadata cache")
	f.StringVar(&r.cacheDir, "cache-dir", "", "cache directory (default: the user cache directory)")
	f.StringVar(&r.repoURL, "repo-url", "", "repository URL (auto-detected in GitLab CI and GitHub Actions)")
	f.StringVar(&r.commit, "commit", "", "commit SHA (auto-detected in CI)")
	f.StringVar(&r.branch, "branch", "", "current branch (auto-detected in CI)")
	f.StringVar(&r.target, "target-branch", "", "target/default branch (auto-detected in CI)")
	f.StringArrayVar(&r.sboms, "sbom", nil, "use a CycloneDX SBOM for a project: <project-dir>=<path> (repeatable)")
	if scan {
		f.BoolVar(&r.allowPartial, "allow-partial", false, "do not fail with exit code 3 when some projects could not be scanned")
		f.StringVar(&r.failOn, "fail-on", "", "exit with code 1 when active findings at or above this severity exist")
		f.StringSliceVar(&r.formats, "format", nil, "report formats (default: html,json)")
		f.StringVar(&r.out, "out", "", "report output directory (default: deepward-out)")
		f.StringArrayVar(&r.sarifs, "import-sarif", nil, "import external SARIF results: <tool>=<path> (repeatable)")
	}
}

// session is a prepared run.
type session struct {
	env    Env
	g      *globalOpts
	root   string
	cfg    engine.Config
	file   string
	eng    *engine.Engine
	tools  sdk.Tools
	ci     CIContext
	logger *slog.Logger
}

func prepare(ctx context.Context, env Env, g *globalOpts, r *runFlags, args []string) (*session, error) {
	logger, err := newLogger(env, g)
	if err != nil {
		return nil, err
	}
	root := env.Cwd
	if len(args) > 0 {
		root = args[0]
		if !filepath.IsAbs(root) {
			root = filepath.Join(env.Cwd, root)
		}
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, usagef("scan root %s is not a directory", root)
	}
	cfgPath := g.config
	if cfgPath != "" && !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(env.Cwd, cfgPath)
	}
	cfg, file, err := loadConfig(root, cfgPath)
	if err != nil {
		return nil, err
	}
	if err := r.apply(&cfg); err != nil {
		return nil, err
	}
	ci := detectCI(env.getenv).override(r.repoURL, r.commit, r.branch, r.target)
	cacheDir := r.cacheDir
	if cacheDir == "" {
		cacheDir = cfg.Cache.Dir
	}
	if cacheDir == "" {
		if d, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(d, "deepward")
		} else {
			cacheDir = filepath.Join(root, ".deepward-cache")
		}
	} else if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(root, cacheDir)
	}
	ws := sdk.NewWorkspace(root, nil)
	reg, err := runRegistry(env.Registry, cfg, filepath.Base(firstNonEmpty(file, DefaultConfig)))
	if err != nil {
		return nil, err
	}
	var tools sdk.Tools
	if env.NewTools != nil {
		tools, err = env.NewTools(ctx, cfg, ws, reg, cacheDir, env)
		if err != nil {
			return nil, err
		}
	}
	eng, err := engine.New(ctx, engine.Options{
		Registry: reg, Config: cfg, Workspace: ws, Tools: tools, Clock: env.Clock, Logger: logger,
		CacheDir: cacheDir, NoCache: r.noCache, ConfigFile: filepath.Base(firstNonEmpty(file, DefaultConfig)),
		Tool: sdk.ToolInfo{Name: "deepward", Version: firstNonEmpty(env.Version, "dev"), Commit: env.Commit},
		Run: sdk.RunInfo{Root: filepath.ToSlash(root), RepoURL: ci.RepoURL, Commit: ci.Commit, Branch: ci.Branch,
			TargetBranch: ci.DefaultBranch, CI: ci.Provider},
	})
	if err != nil {
		return nil, usage(err)
	}
	return &session{env: env, g: g, root: root, cfg: cfg, file: file, eng: eng, tools: tools, ci: ci, logger: logger}, nil
}

// runRegistry copies the registered plugins and adds the config-defined
// integrations, so each run has its own registry.
func runRegistry(base *sdk.Registry, cfg engine.Config, file string) (*sdk.Registry, error) {
	reg := sdk.NewRegistry()
	for _, p := range base.All() {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	plugins, err := integrations.Compile(cfg.Integrations, file, reg)
	if err != nil {
		return nil, usage(err)
	}
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			return nil, usage(err)
		}
	}
	return reg, nil
}

// apply layers flag values on top of the configuration file.
func (r *runFlags) apply(cfg *engine.Config) error {
	if r.offline {
		cfg.Offline = true
	}
	if r.allowPartial {
		cfg.AllowPartial = true
	}
	if r.failOn != "" {
		s, err := severityFlag(r.failOn)
		if err != nil {
			return err
		}
		cfg.FailOn = s
	}
	if len(r.formats) > 0 {
		cfg.Output.Formats = r.formats
	}
	if r.out != "" {
		cfg.Output.Dir = r.out
	}
	if r.cacheDir != "" {
		cfg.Cache.Dir = r.cacheDir
	}
	sboms, err := parsePairs("sbom", r.sboms)
	if err != nil {
		return err
	}
	for _, p := range sboms {
		setPluginEntry(cfg, "sbom-import", "files", filepath.ToSlash(filepath.Clean(p[0])), p[1])
	}
	sarifs, err := parsePairs("import-sarif", r.sarifs)
	if err != nil {
		return err
	}
	for _, p := range sarifs {
		setPluginEntry(cfg, "sarif-import", "files", p[0], p[1])
	}
	return nil
}
