package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Builtin resolver IDs.
const (
	ResolverConfigPath = "config-path"
	ResolverWrapper    = "wrapper"
	ResolverPath       = "path"
	ResolverContainer  = "container"
	ResolverDownload   = "download"
)

// DefaultOrder is the resolver order when a tool does not configure one.
var DefaultOrder = []string{ResolverConfigPath, ResolverWrapper, ResolverPath}

// Builtins are the tools known without any plugin.
var Builtins = []sdk.ToolDefinition{
	{Name: "mvn", Executables: []string{"mvn", "mvn.cmd"}, Wrappers: []string{"mvnw", "mvnw.cmd"},
		VersionArgs: []string{"-v", "-B"}, VersionPattern: `Apache Maven (\S+)`, JVMOptsEnv: "MAVEN_OPTS"},
	{Name: "sbt", Executables: []string{"sbt", "sbt.bat"}, Wrappers: []string{"sbtx", "sbtw", "sbt"},
		VersionArgs: []string{"--script-version"}, VersionPattern: `(\d+\.\d+\.\d+\S*)`, JVMOptsEnv: "SBT_OPTS"},
	{Name: "dotnet", Executables: []string{"dotnet", "dotnet.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `^\s*(\S+)`},
	{Name: "npm", Executables: []string{"npm", "npm.cmd"}, VersionArgs: []string{"--version"}, VersionPattern: `^\s*v?(\d+\.\d+\.\d+\S*)`},
	{Name: "yarn", Executables: []string{"yarn", "yarn.cmd"}, VersionArgs: []string{"--version"}, VersionPattern: `^\s*v?(\d+\.\d+\.\d+\S*)`},
	{Name: "pnpm", Executables: []string{"pnpm", "pnpm.cmd", "pnpm.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `^\s*v?(\d+\.\d+\.\d+\S*)`},
	{Name: "go", Executables: []string{"go", "go.exe"}, VersionArgs: []string{"version"}, VersionPattern: `go version go(\d+\.\d+(?:\.\d+)?\S*)`},
	{Name: "cargo", Executables: []string{"cargo", "cargo.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `cargo (\d+\.\d+\.\d+\S*)`},
	{Name: "java", Executables: []string{"java", "java.exe"}, VersionArgs: []string{"-version"}, VersionPattern: `version "([^"]+)"`},
	{Name: "git", Executables: []string{"git", "git.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `git version (\S+)`},
	{Name: "docker", Executables: []string{"docker", "docker.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `Docker version ([^,\s]+)`},
	{Name: "podman", Executables: []string{"podman", "podman.exe"}, VersionArgs: []string{"--version"}, VersionPattern: `podman version (\S+)`},
}

// Options configure a toolchain.
type Options struct {
	// Resolvers are the enabled resolver plugins.
	Resolvers []sdk.ToolResolver
	// Definitions are tool definitions contributed by plugins.
	Definitions []sdk.ToolDefinition
	// Settings are tools.<name> sections.
	Settings  map[string]sdk.ToolSettings
	Workspace *sdk.Workspace
	CacheDir  string
	// Environ is the parent environment ("K=V").
	Environ []string
	Getenv  func(string) string
	// Secrets are values always masked in output.
	Secrets []string
	// ProbeTimeout bounds version probes (default 2m).
	ProbeTimeout time.Duration
}

// Toolchain implements sdk.Tools.
type Toolchain struct {
	opts      Options
	resolvers map[string]sdk.ToolResolver
	defs      map[string]sdk.ToolDefinition
	mu        sync.Mutex
	cache     map[string]*Diagnosis
}

// New validates settings and builds a toolchain.
func New(opts Options) (*Toolchain, error) {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = 2 * time.Minute
	}
	t := &Toolchain{opts: opts, resolvers: map[string]sdk.ToolResolver{}, defs: map[string]sdk.ToolDefinition{}, cache: map[string]*Diagnosis{}}
	for _, r := range opts.Resolvers {
		t.resolvers[r.Info().ID] = r
	}
	for _, d := range Builtins {
		t.defs[d.Name] = d
	}
	for _, d := range opts.Definitions {
		t.defs[d.Name] = mergeDefinition(t.defs[d.Name], d)
	}
	var errs []error
	for _, name := range sortedNames(opts.Settings) {
		s := opts.Settings[name]
		if len(s.Executables) > 0 || s.VersionPattern != "" || len(s.VersionArgs) > 0 {
			t.defs[name] = mergeDefinition(t.defs[name], sdk.ToolDefinition{Name: name, Executables: s.Executables,
				VersionArgs: s.VersionArgs, VersionPattern: s.VersionPattern})
		}
		if s.VersionPattern != "" {
			if _, err := regexp.Compile(s.VersionPattern); err != nil {
				errs = append(errs, fmt.Errorf("tools.%s.versionPattern: %w", name, err))
			}
		}
		for _, r := range s.Resolvers {
			if _, ok := t.resolvers[r]; !ok {
				errs = append(errs, fmt.Errorf("tools.%s.resolvers: unknown resolver %q", name, r))
			}
		}
		for _, r := range t.order(s) {
			if r == ResolverContainer && s.Image == "" {
				errs = append(errs, fmt.Errorf("tools.%s.image is required for the container resolver", name))
			}
			if r == ResolverDownload && (s.Download == nil || s.Download.URL == "") {
				errs = append(errs, fmt.Errorf("tools.%s.download.url is required for the download resolver", name))
			}
		}
		if s.Download != nil && s.Download.SHA256 == "" {
			errs = append(errs, fmt.Errorf("tools.%s.download.sha256 is required: downloads without a checksum are refused", name))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return t, nil
}

func mergeDefinition(base, d sdk.ToolDefinition) sdk.ToolDefinition {
	base.Name = d.Name
	if len(d.Executables) > 0 {
		base.Executables = d.Executables
	}
	if len(d.Wrappers) > 0 {
		base.Wrappers = d.Wrappers
	}
	if len(d.VersionArgs) > 0 {
		base.VersionArgs = d.VersionArgs
	}
	if d.VersionPattern != "" {
		base.VersionPattern = d.VersionPattern
	}
	if d.JVMOptsEnv != "" {
		base.JVMOptsEnv = d.JVMOptsEnv
	}
	return base
}

func sortedNames(m map[string]sdk.ToolSettings) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// order returns the resolver order for a tool. container and download are
// used only when configured.
func (t *Toolchain) order(s sdk.ToolSettings) []string {
	if len(s.Resolvers) > 0 {
		return s.Resolvers
	}
	order := append([]string{}, DefaultOrder...)
	if s.Image != "" {
		order = append(order, ResolverContainer)
	}
	if s.Download != nil {
		order = append(order, ResolverDownload)
	}
	return order
}

func (t *Toolchain) definition(name string) sdk.ToolDefinition {
	if d, ok := t.defs[name]; ok {
		return d
	}
	return sdk.ToolDefinition{Name: name, Executables: []string{name}}
}

// Attempt is one resolver's outcome.
type Attempt struct {
	Resolver string `json:"resolver"`
	Outcome  string `json:"outcome"`
}

// Diagnosis is the full resolution outcome of a tool.
type Diagnosis struct {
	Tool       string    `json:"tool"`
	Constraint string    `json:"constraint,omitempty"`
	Resolver   string    `json:"resolver,omitempty"`
	Source     string    `json:"source,omitempty"`
	Version    string    `json:"version,omitempty"`
	OK         bool      `json:"ok"`
	Attempts   []Attempt `json:"attempts"`
	Err        error     `json:"-"`
	candidate  *sdk.ToolCandidate
}

// Diagnose resolves a tool and reports every attempt.
func (t *Toolchain) Diagnose(ctx context.Context, req sdk.ToolRequirement, project *sdk.Project) *Diagnosis {
	key := req.Name + "\x00" + req.Constraint
	if project != nil {
		key += "\x00" + project.Dir
	}
	t.mu.Lock()
	if d, ok := t.cache[key]; ok {
		t.mu.Unlock()
		return d
	}
	t.mu.Unlock()
	d := t.resolve(ctx, req, project)
	t.mu.Lock()
	t.cache[key] = d
	t.mu.Unlock()
	return d
}

func (t *Toolchain) resolve(ctx context.Context, req sdk.ToolRequirement, project *sdk.Project) *Diagnosis {
	d := &Diagnosis{Tool: req.Name, Constraint: req.Constraint}
	constraint, err := ParseConstraint(req.Constraint)
	if err != nil {
		d.Err = &sdk.ToolError{Tool: req.Name, Reason: sdk.ErrToolVersionMismatch, Detail: err.Error()}
		return d
	}
	def := t.definition(req.Name)
	settings := t.opts.Settings[req.Name]
	var mismatch string
	for _, id := range t.order(settings) {
		r, ok := t.resolvers[id]
		if !ok {
			d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: "resolver not available"})
			continue
		}
		cand, err := r.Resolve(ctx, sdk.ResolveRequest{Definition: def, Settings: settings, Project: project,
			Workspace: t.opts.Workspace, CacheDir: filepath.Join(t.opts.CacheDir, "tools")})
		if err != nil {
			d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: "error: " + err.Error()})
			continue
		}
		if cand == nil || len(cand.Command) == 0 {
			d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: "not found"})
			continue
		}
		version, err := t.probe(ctx, def, settings, cand)
		if err != nil {
			d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: "version probe failed: " + err.Error()})
			continue
		}
		if !constraint.Allows(version) && constraint.String() != "" {
			mismatch = fmt.Sprintf("%s %s does not satisfy %s", req.Name, firstNonEmpty(version, "(unknown version)"), constraint)
			d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: mismatch})
			continue
		}
		d.Attempts = append(d.Attempts, Attempt{Resolver: id, Outcome: "found " + cand.Source})
		d.Resolver, d.Source, d.Version, d.OK, d.candidate = id, cand.Source, version, true, cand
		return d
	}
	if mismatch != "" {
		d.Err = &sdk.ToolError{Tool: req.Name, Reason: sdk.ErrToolVersionMismatch, Detail: mismatch}
		return d
	}
	tried := make([]string, 0, len(d.Attempts))
	for _, a := range d.Attempts {
		tried = append(tried, a.Resolver)
	}
	d.Err = &sdk.ToolError{Tool: req.Name, Reason: sdk.ErrToolNotFound, Detail: fmt.Sprintf("%s (tried: %s)", req.Name, strings.Join(tried, ", "))}
	return d
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func (t *Toolchain) probe(ctx context.Context, def sdk.ToolDefinition, s sdk.ToolSettings, cand *sdk.ToolCandidate) (string, error) {
	if len(def.VersionArgs) == 0 || def.VersionPattern == "" {
		return "", nil
	}
	re, err := regexp.Compile("(?m)" + def.VersionPattern)
	if err != nil {
		return "", err
	}
	tool := &tool{t: t, def: def, settings: s, cand: cand}
	res, err := tool.run(ctx, sdk.RunSpec{Args: def.VersionArgs, Timeout: t.opts.ProbeTimeout}, false)
	text := string(res.Stdout) + "\n" + strings.Join(res.Tail, "\n")
	if err != nil {
		var xe *sdk.ExitError
		if !errors.As(err, &xe) {
			return "", err
		}
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("version not found in output of %s %s", def.Name, strings.Join(def.VersionArgs, " "))
	}
	return m[1], nil
}

// Require implements sdk.Tools.
func (t *Toolchain) Require(ctx context.Context, req sdk.ToolRequirement, project *sdk.Project) (sdk.Tool, error) {
	d := t.Diagnose(ctx, req, project)
	if !d.OK {
		return nil, d.Err
	}
	return &tool{t: t, def: t.definition(req.Name), settings: t.opts.Settings[req.Name], cand: d.candidate, version: d.Version, resolver: d.Resolver}, nil
}

type tool struct {
	t        *Toolchain
	def      sdk.ToolDefinition
	settings sdk.ToolSettings
	cand     *sdk.ToolCandidate
	version  string
	resolver string
}

func (x *tool) Name() string      { return x.def.Name }
func (x *tool) Version() string   { return x.version }
func (x *tool) Resolver() string  { return x.resolver }
func (x *tool) Command() []string { return append([]string{}, x.cand.Command...) }

// Run implements sdk.Tool.
func (x *tool) Run(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
	return x.run(ctx, spec, true)
}

func (x *tool) run(ctx context.Context, spec sdk.RunSpec, withSettingsArgs bool) (sdk.RunResult, error) {
	getenv := x.t.opts.Getenv
	secrets := append([]string{}, x.t.opts.Secrets...)
	secrets = append(secrets, spec.Secrets...)
	toolEnv := map[string]string{}
	for k, v := range x.cand.Env {
		toolEnv[k] = v
	}
	if x.settings.JavaHome != "" {
		toolEnv["JAVA_HOME"] = x.settings.JavaHome
	}
	if x.settings.JVMOpts != "" {
		toolEnv[firstNonEmpty(x.def.JVMOptsEnv, "JAVA_OPTS")] = x.settings.JVMOpts
	}
	for k, v := range x.settings.Env {
		for _, m := range envRef.FindAllStringSubmatch(v, -1) {
			if val := getenv(m[1]); val != "" {
				secrets = append(secrets, val)
			}
		}
		expanded := envRef.ReplaceAllStringFunc(v, func(m string) string { return getenv(envRef.FindStringSubmatch(m)[1]) })
		toolEnv[k] = expanded
	}
	for k, v := range spec.Env {
		toolEnv[k] = v
	}
	env := filterEnv(x.t.opts.Environ)
	for k, v := range toolEnv {
		env[k] = v
	}
	if jh := toolEnv["JAVA_HOME"]; jh != "" {
		env["PATH"] = filepath.Join(jh, "bin") + string(os.PathListSeparator) + env["PATH"]
	}
	root := ""
	if x.t.opts.Workspace != nil {
		root = x.t.opts.Workspace.Root
	}
	dir := spec.Dir
	if dir == "" {
		dir = root
		if x.settings.Workdir != "" {
			dir = filepath.Join(root, filepath.FromSlash(x.settings.Workdir))
		}
	}
	mapPath := x.cand.MapPath
	if mapPath == nil {
		mapPath = func(p string) string { return p }
	}
	var argv []string
	for _, a := range x.cand.Command {
		switch a {
		case "{{dir}}":
			argv = append(argv, mapPath(dir))
		case "{{dirmount}}":
			// Runs outside the workspace (e.g. the private copies `fix`
			// verifies) need their directory mounted too.
			if rel, err := filepath.Rel(root, dir); root == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				argv = append(argv, "-v", mapPath(dir)+":"+mapPath(dir))
			}
		case "{{env}}":
			for _, k := range sortedKeys(toolEnv) {
				argv = append(argv, "-e", k)
			}
		default:
			argv = append(argv, a)
		}
	}
	if withSettingsArgs {
		argv = append(argv, x.settings.Args...)
	}
	argv = append(argv, spec.Args...)
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = x.settings.Timeout.Std()
	}
	return execute(ctx, process{name: x.def.Name, argv: argv, dir: dir, env: env, stdin: spec.Stdin, timeout: timeout, secrets: secrets})
}

var envRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
