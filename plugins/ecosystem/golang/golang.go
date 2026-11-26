// Package golang is the Go modules ecosystem plugin: it detects go.mod
// modules, builds their dependency graphs with the Go toolchain, lists
// versions from module proxies and realizes updates with `go get`.
package golang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/golang/domain"
)

// ID is the plugin ID.
const ID = "go"

// MinGo is the minimal Go toolchain (pruned module graphs, -mod=readonly).
const MinGo = ">=1.17"

// Config is plugins.go.
type Config struct {
	// Proxy overrides GOPROXY for version discovery.
	Proxy string `yaml:"proxy" json:"proxy,omitempty"`
	// Private overrides GOPRIVATE (and GONOPROXY) for version discovery.
	Private string `yaml:"private" json:"private,omitempty"`
	// Platforms are the GOOS/GOARCH pairs whose builds decide which modules
	// are compiled (default linux/amd64, darwin/arm64, windows/amd64).
	Platforms []string `yaml:"platforms" json:"platforms,omitempty"`
	// Tags are build tags the project builds with (passed as -tags).
	Tags     []string     `yaml:"tags" json:"tags,omitempty"`
	CacheTTL sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
}

// DefaultPlatforms decide which modules are compiled when none are configured.
var DefaultPlatforms = []string{"linux/amd64", "darwin/arm64", "windows/amd64"}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, pl := range c.Platforms {
		if goos, goarch, ok := strings.Cut(pl, "/"); !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return fmt.Errorf("platforms[%d]: %q must be GOOS/GOARCH", i, pl)
		}
	}
	return nil
}

// Plugin is the Go modules ecosystem.
type Plugin struct {
	cfg    Config
	host   sdk.Host
	http   *deps.HTTPGetter
	Getenv func(string) string
	HTTP   *http.Client
}

func init() { sdk.Register(New()) }

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: "Go modules: graphs via go list -m and go mod graph, versions from module proxies, updates via go get"}
}

// ConfigTarget implements sdk.Configurable.
func (p *Plugin) ConfigTarget() any { return &p.cfg }

func (p *Plugin) getenv(k string) string {
	if p.Getenv != nil {
		return p.Getenv(k)
	}
	return os.Getenv(k)
}

// Init implements sdk.Initializer.
func (p *Plugin) Init(_ context.Context, h sdk.Host) error {
	p.host = h
	p.http = deps.NewHTTPGetter(h, p.HTTP, p.cfg.CacheTTL.Std())
	return nil
}

// ToolRequirements implements sdk.ToolRequirer.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: "go", Constraint: MinGo, Purpose: "build the module graph and update go.mod"}}
}

// skipDir reports directories the go command ignores.
func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Detect implements sdk.Ecosystem: one project per go.mod.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var projects []sdk.Project
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() {
			if rel != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := path.Dir(rel)
		data, err := fs.ReadFile(ws.FS, rel)
		if err != nil {
			return err
		}
		manifests := []string{rel}
		if _, err := fs.Stat(ws.FS, path.Join(dir, "go.sum")); err == nil {
			manifests = append(manifests, path.Join(dir, "go.sum"))
		}
		attrs := map[string]string{"module": modfile.ModulePath(data)}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, dir), Ecosystem: ID, Dir: dir, Manifests: manifests, Attributes: attrs})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// goEnv keeps the go command on this module only, without network prompts.
var goEnv = map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=mod", "GO111MODULE": "on"}

// Produce implements sdk.FactProducer.
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("go cannot produce %s", req.Name)
	}
	if p.host == nil {
		return nil, errors.New("go plugin not initialized")
	}
	project := req.Project
	tool, err := p.host.Tools().Require(ctx, sdk.ToolRequirement{Name: "go", Constraint: MinGo}, &project)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(req.Workspace.Root, filepath.FromSlash(project.Dir))
	env := map[string]string{"GOWORK": "off", "GOFLAGS": "-mod=readonly", "GO111MODULE": "on"}
	list, err := tool.Run(ctx, sdk.RunSpec{Args: []string{"list", "-m", "-json", "all"}, Dir: dir, Env: env})
	if err != nil {
		return nil, fmt.Errorf("go list -m all failed: %w", err)
	}
	mods, err := domain.ParseList(bytes.NewReader(list.Stdout))
	if err != nil {
		return nil, err
	}
	graph, err := tool.Run(ctx, sdk.RunSpec{Args: []string{"mod", "graph"}, Dir: dir, Env: env})
	if err != nil {
		return nil, fmt.Errorf("go mod graph failed: %w", err)
	}
	scopes := p.scopes(ctx, tool, dir, env)
	return domain.BuildGraph("project:"+project.ID, "go list -m all, go mod graph", mods, graph.Stdout, scopes)
}

// scopes lists the modules whose packages are compiled, by the module's
// packages and by their tests, for every configured platform. It returns nil
// (every module counts as compiled) when no build listing succeeds.
func (p *Plugin) scopes(ctx context.Context, tool sdk.Tool, dir string, base map[string]string) map[string]string {
	platforms := p.cfg.Platforms
	if len(platforms) == 0 {
		platforms = DefaultPlatforms
	}
	built, withTests := map[string]bool{}, map[string]bool{}
	for _, pl := range platforms {
		goos, goarch, _ := strings.Cut(pl, "/")
		env := map[string]string{"GOOS": goos, "GOARCH": goarch}
		for k, v := range base {
			env[k] = v
		}
		for _, test := range []bool{false, true} {
			args := []string{"list", "-e", "-deps"}
			if test {
				args = append(args, "-test")
			}
			if len(p.cfg.Tags) > 0 {
				args = append(args, "-tags", strings.Join(p.cfg.Tags, ","))
			}
			args = append(args, "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
			res, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir, Env: env})
			if err != nil {
				continue
			}
			target := built
			if test {
				target = withTests
			}
			for m := range domain.ModuleList(res.Stdout) {
				target[m] = true
			}
		}
	}
	return domain.Scopes(built, withTests)
}

// Versions implements deps.VersionRegistry: it asks the GOPROXY proxies for
// the versions of the same module path.
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.http == nil {
		return nil, errors.New("go plugin not initialized")
	}
	mod := deps.PackageName(pu)
	private := p.cfg.Private
	noproxy := ""
	if private == "" {
		private, noproxy = p.getenv("GOPRIVATE"), p.getenv("GONOPROXY")
	}
	if domain.Private(noproxy, private, mod) {
		return nil, deps.ErrPackageNotFound
	}
	proxy := p.cfg.Proxy
	if proxy == "" {
		proxy = p.getenv("GOPROXY")
	}
	var errs []error
	for _, px := range domain.ParseGOPROXY(proxy) {
		u, err := domain.ListURL(px.URL, mod)
		if err != nil {
			return nil, err
		}
		data, status, err := p.http.Get(ctx, u, nil)
		switch {
		case err == nil && status == http.StatusOK:
			var out []string
			for _, line := range strings.Split(string(data), "\n") {
				if v := strings.TrimSpace(line); v != "" {
					out = append(out, v)
				}
			}
			sort.Strings(out)
			return out, nil
		case err == nil && (status == http.StatusNotFound || status == http.StatusGone):
			continue
		case err == nil:
			err = fmt.Errorf("HTTP %d", status)
		}
		errs = append(errs, fmt.Errorf("proxy %s: %w", px.URL, err))
		if !px.FallbackOnError {
			return nil, errors.Join(errs...)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, deps.ErrPackageNotFound
}

// Realize implements sdk.RemediationRealizer with `go get module@version`.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	project := req.Project
	root := req.Workspace.Root
	gomod := path.Join(project.Dir, "go.mod")
	data, err := deps.ReadFile(root, gomod)
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	mf, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	dir := filepath.Join(root, filepath.FromSlash(project.Dir))
	vendored := false
	if _, err := os.Stat(filepath.Join(dir, "vendor", "modules.txt")); err == nil {
		vendored = true
	}
	var res sdk.RealizeResult
	var targets []string
	for _, c := range req.Changes {
		pu, err := purl.Parse(c.PURL)
		if err != nil {
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
			continue
		}
		mod := deps.PackageName(pu)
		target := mod + "@" + domain.CanonicalVersion(c.To)
		suggestion := "go get " + target
		replaced := false
		for _, r := range mf.Replace {
			if r.Old.Path == mod {
				replaced = true
			}
		}
		switch {
		case replaced:
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: mod + " is replaced in go.mod", Suggestion: "update the replace directive for " + mod})
		case vendored:
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "the module vendors its dependencies", Suggestion: suggestion + " && go mod vendor"})
		default:
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return res, nil
	}
	if p.host == nil {
		return sdk.RealizeResult{}, errors.New("go plugin not initialized")
	}
	tool, err := p.host.Tools().Require(ctx, sdk.ToolRequirement{Name: "go", Constraint: MinGo}, &project)
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	files := []string{gomod, path.Join(project.Dir, "go.sum")}
	edits, err := deps.Regenerate(ctx, root, files, func(ctx context.Context) error {
		if _, err := tool.Run(ctx, sdk.RunSpec{Args: append([]string{"get"}, targets...), Dir: dir, Env: goEnv}); err != nil {
			return fmt.Errorf("go get %s: %w", strings.Join(targets, " "), tailError(err))
		}
		return nil
	})
	if err != nil {
		return sdk.RealizeResult{}, err
	}
	res.Edits = edits
	return res, nil
}

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
