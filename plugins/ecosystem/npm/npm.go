// Package npm is the Node.js ecosystem plugin: it detects npm, Yarn and pnpm
// projects (including workspaces), builds their dependency graphs from the
// committed lockfile, lists versions from npm registries and realizes
// updates by editing package.json and regenerating the lockfile with the
// project's package manager.
package npm

import (
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

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/npm/domain"
)

// ID is the plugin ID.
const ID = "npm"

// DefaultRegistry is the public npm registry.
const DefaultRegistry = "https://registry.npmjs.org"

// Registry configures an npm registry for version discovery.
type Registry struct {
	URL string `yaml:"url" json:"url"`
	// Scope limits the registry to one scope (e.g. "@acme").
	Scope       string `yaml:"scope" json:"scope,omitempty"`
	TokenEnv    string `yaml:"tokenEnv" json:"tokenEnv,omitempty"`
	UsernameEnv string `yaml:"usernameEnv" json:"usernameEnv,omitempty"`
	PasswordEnv string `yaml:"passwordEnv" json:"passwordEnv,omitempty"`
}

// Config is plugins.npm.
type Config struct {
	// Registries are queried for versions (default: registry.npmjs.org).
	Registries []Registry   `yaml:"registries" json:"registries"`
	CacheTTL   sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, r := range c.Registries {
		if r.URL == "" {
			return fmt.Errorf("registries[%d].url is required", i)
		}
		if r.Scope != "" && !strings.HasPrefix(r.Scope, "@") {
			return fmt.Errorf("registries[%d].scope must start with @", i)
		}
	}
	return nil
}

// Plugin is the npm ecosystem.
type Plugin struct {
	cfg      Config
	host     sdk.Host
	registry *registryClient
	Getenv   func(string) string
	HTTP     *http.Client
}

func init() { sdk.Register(New()) }

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: "Node.js projects (npm, Yarn, pnpm, workspaces): graphs from lockfiles, versions from npm registries, package.json and lockfile updates"}
}

// ConfigTarget implements sdk.Configurable.
func (p *Plugin) ConfigTarget() any { return &p.cfg }

func (p *Plugin) getenv(k string) string {
	if k == "" {
		return ""
	}
	if p.Getenv != nil {
		return p.Getenv(k)
	}
	return os.Getenv(k)
}

// Init implements sdk.Initializer.
func (p *Plugin) Init(_ context.Context, h sdk.Host) error {
	p.host = h
	regs := p.cfg.Registries
	hasDefault := false
	for _, r := range regs {
		if r.Scope == "" {
			hasDefault = true
		}
	}
	if !hasDefault {
		regs = append(append([]Registry{}, regs...), Registry{URL: DefaultRegistry})
	}
	var rs []registry
	for _, r := range regs {
		rs = append(rs, registry{URL: strings.TrimSuffix(r.URL, "/"), Scope: r.Scope, Token: p.getenv(r.TokenEnv),
			Username: p.getenv(r.UsernameEnv), Password: p.getenv(r.PasswordEnv)})
	}
	p.registry = &registryClient{registries: rs, http: deps.NewHTTPGetter(h, p.HTTP, p.cfg.CacheTTL.Std())}
	return nil
}

// ToolRequirements implements sdk.ToolRequirer. The package managers are
// only needed without a lockfile and to realize fixes.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement { return nil }

// managerRequirement returns the tool a project's package manager needs.
func managerRequirement(m domain.Manager) sdk.ToolRequirement {
	return sdk.ToolRequirement{Name: string(m), Purpose: "update the lockfile"}
}

var extraManifests = []string{"pnpm-workspace.yaml", ".npmrc", ".yarnrc", ".yarnrc.yml", ".pnpmfile.cjs"}

type rootInfo struct {
	dir      string
	lockfile string
	manager  domain.Manager
	patterns []string
	members  []string
}

// Detect implements sdk.Ecosystem.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifests := map[string]*domain.Manifest{}
	files := map[string]bool{}
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		files[rel] = true
		if path.Base(rel) == "package.json" {
			data, err := fs.ReadFile(ws.FS, rel)
			if err != nil {
				return err
			}
			m, err := domain.ParseManifest(rel, data)
			if err != nil {
				// A broken package.json is reported when the graph is built.
				m = &domain.Manifest{Path: rel, Deps: map[domain.DepKind]map[string]string{}}
			}
			manifests[path.Dir(rel)] = m
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	roots := map[string]*rootInfo{}
	join := func(dir, name string) string {
		if dir == "." {
			return name
		}
		return dir + "/" + name
	}
	for dir, m := range manifests {
		r := &rootInfo{dir: dir}
		for _, lf := range domain.Lockfiles {
			if files[join(dir, lf.Name)] {
				r.lockfile, r.manager = join(dir, lf.Name), lf.Manager
				break
			}
		}
		if files[join(dir, "pnpm-workspace.yaml")] {
			data, _ := fs.ReadFile(ws.FS, join(dir, "pnpm-workspace.yaml"))
			var pw struct {
				Packages []string `yaml:"packages"`
			}
			_ = yaml.Unmarshal(data, &pw)
			r.patterns = pw.Packages
			if r.manager == "" {
				r.manager = domain.PNPM
			}
		} else {
			r.patterns = m.Workspaces
		}
		if r.lockfile != "" || r.patterns != nil || files[join(dir, "pnpm-workspace.yaml")] {
			if r.manager == "" {
				r.manager = managerOf(m)
			}
			roots[dir] = r
		}
	}
	nearestRoot := func(dir string) *rootInfo {
		for d := dir; ; d = path.Dir(d) {
			if r, ok := roots[d]; ok {
				return r
			}
			if d == "." || d == "/" {
				return nil
			}
		}
	}
	for _, dir := range domain.SortedKeys(manifests) {
		r := nearestRoot(dir)
		switch {
		case r == nil:
			// A standalone package without lockfile.
			roots[dir] = &rootInfo{dir: dir, manager: managerOf(manifests[dir]), members: []string{dir}}
		case r.dir == dir:
			r.members = append([]string{dir}, r.members...)
		default:
			rel := strings.TrimPrefix(dir, r.dir+"/")
			if r.dir == "." {
				rel = dir
			}
			if domain.MatchWorkspace(r.patterns, rel) {
				r.members = append(r.members, dir)
			}
		}
	}
	var projects []sdk.Project
	for _, r := range roots {
		if len(r.members) == 0 {
			continue
		}
		var ms []string
		hasDeps := false
		for _, d := range r.members {
			ms = append(ms, manifests[d].Path)
			hasDeps = hasDeps || len(manifests[d].DirectDeps()) > 0
		}
		if r.lockfile == "" && !hasDeps {
			// Nothing to analyze, and no reason to run a package manager.
			continue
		}
		if r.lockfile != "" {
			ms = append(ms, r.lockfile)
		}
		for _, e := range extraManifests {
			if files[join(r.dir, e)] {
				ms = append(ms, join(r.dir, e))
			}
		}
		attrs := map[string]string{"packageManager": string(r.manager), "lockfile": r.lockfile, "packages": strings.Join(r.members, ",")}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, r.dir), Ecosystem: ID, Dir: r.dir, Manifests: ms, Attributes: attrs})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

func managerOf(m *domain.Manifest) domain.Manager {
	if mg, ok := domain.ManagerFromField(m.PackageManager); ok {
		return mg
	}
	return domain.NPM
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// projectManifests reads the package.json files of a project, keyed by
// directory relative to the project directory.
func projectManifests(fsys fs.FS, project sdk.Project) (map[string]*domain.Manifest, error) {
	out := map[string]*domain.Manifest{}
	for _, dir := range strings.Split(project.Attributes["packages"], ",") {
		if dir == "" {
			continue
		}
		file := path.Join(dir, "package.json")
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		m, err := domain.ParseManifest(file, data)
		if err != nil {
			return nil, err
		}
		rel := "."
		if dir != project.Dir {
			rel = strings.TrimPrefix(dir, project.Dir+"/")
			if project.Dir == "." {
				rel = dir
			}
		}
		out[rel] = m
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no package.json in project %s", project.ID)
	}
	return out, nil
}

// Produce implements sdk.FactProducer.
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("npm cannot produce %s", req.Name)
	}
	project := req.Project
	manifests, err := projectManifests(req.Workspace.FS, project)
	if err != nil {
		return nil, err
	}
	manager := domain.Manager(project.Attributes["packageManager"])
	lockfile := project.Attributes["lockfile"]
	var data []byte
	if lockfile != "" {
		if data, err = fs.ReadFile(req.Workspace.FS, lockfile); err != nil {
			return nil, err
		}
	} else {
		if p.host == nil {
			return nil, errors.New("npm plugin not initialized")
		}
		if lockfile, data, err = p.generateLockfile(ctx, req.Workspace, project, manager); err != nil {
			return nil, err
		}
	}
	tree, err := parseLock(path.Base(lockfile), data, manifests)
	if err != nil {
		return nil, err
	}
	return tree.Graph("project:"+project.ID, tree.Format)
}

func parseLock(name string, data []byte, manifests map[string]*domain.Manifest) (*domain.Tree, error) {
	switch name {
	case "package-lock.json", "npm-shrinkwrap.json":
		return domain.ParseNPMLock(data, manifests["."])
	case "yarn.lock":
		return domain.ParseYarnLock(data, manifests)
	case "pnpm-lock.yaml":
		return domain.ParsePNPMLock(data)
	}
	return nil, fmt.Errorf("unknown lockfile %s", name)
}

// lockfileName is the lockfile each package manager writes.
func lockfileName(m domain.Manager) string {
	switch m {
	case domain.Yarn:
		return "yarn.lock"
	case domain.PNPM:
		return "pnpm-lock.yaml"
	}
	return "package-lock.json"
}

// lockArgs returns the arguments that update the lockfile without
// installing packages (where the package manager supports it) or running
// scripts. modules is a scratch directory for Yarn classic, which always
// installs.
func lockArgs(m domain.Manager, tool sdk.Tool, modules string) []string {
	switch m {
	case domain.PNPM:
		return []string{"install", "--lockfile-only", "--ignore-scripts", "--no-frozen-lockfile"}
	case domain.Yarn:
		if yarnBerry(tool) {
			return []string{"install", "--mode=update-lockfile", "--no-immutable"}
		}
		return []string{"install", "--ignore-scripts", "--non-interactive", "--no-progress", "--modules-folder", modules}
	}
	return []string{"install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"}
}

func yarnBerry(tool sdk.Tool) bool {
	v, err := deps.ParseVersion(deps.NPM, tool.Version())
	if err != nil {
		return false
	}
	major, _, _, _ := v.Segments()
	return major >= 2
}

// runLock runs the package manager's lockfile update in dir.
func runLock(ctx context.Context, tool sdk.Tool, m domain.Manager, dir string) error {
	modules, err := os.MkdirTemp("", "deepward-yarn-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(modules)
	env := map[string]string{"COREPACK_ENABLE_DOWNLOAD_PROMPT": "0", "npm_config_update_notifier": "false"}
	if _, err := tool.Run(ctx, sdk.RunSpec{Args: lockArgs(m, tool, modules), Dir: dir, Env: env}); err != nil {
		return fmt.Errorf("%s failed to update the lockfile: %w", m, err)
	}
	return nil
}

// generateLockfile creates a lockfile for a project without one, reads it and
// removes every file the package manager created.
func (p *Plugin) generateLockfile(ctx context.Context, ws *sdk.Workspace, project sdk.Project, m domain.Manager) (string, []byte, error) {
	tool, err := p.host.Tools().Require(ctx, managerRequirement(m), &project)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(ws.Root, filepath.FromSlash(project.Dir))
	before := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	for _, e := range entries {
		before[e.Name()] = true
	}
	defer func() {
		after, _ := os.ReadDir(dir)
		for _, e := range after {
			if !before[e.Name()] {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}()
	if err := runLock(ctx, tool, m, dir); err != nil {
		return "", nil, err
	}
	name := lockfileName(m)
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", nil, fmt.Errorf("%s did not write %s: %w", m, name, err)
	}
	return path.Join(project.Dir, name), data, nil
}

// Versions implements deps.VersionRegistry.
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.registry == nil {
		return nil, errors.New("npm plugin not initialized")
	}
	return p.registry.Versions(ctx, deps.PackageName(pu))
}
