// Package cargo is the Rust ecosystem plugin: it detects crates and
// workspaces, builds their dependency graphs from Cargo.lock, lists versions
// from sparse registry indexes (crates.io by default) and realizes updates
// with `cargo update --precise`, bumping Cargo.toml requirements when needed.
package cargo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/cargo/domain"
)

// ID is the plugin ID.
const ID = "cargo"

// CratesIO is the crates.io sparse index.
const CratesIO = "https://index.crates.io"

// MinCargo is the minimal Cargo version (`-p name@version` package specs).
const MinCargo = ">=1.64"

// Registry configures a sparse registry index for version discovery.
type Registry struct {
	Name string `yaml:"name" json:"name,omitempty"`
	// Index is the sparse index URL (with or without the "sparse+" prefix).
	Index    string `yaml:"index" json:"index"`
	TokenEnv string `yaml:"tokenEnv" json:"tokenEnv,omitempty"`
}

// Config is plugins.cargo.
type Config struct {
	// Registries are asked in order before crates.io; the first registry that
	// knows a crate answers.
	Registries []Registry   `yaml:"registries" json:"registries"`
	CacheTTL   sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, r := range c.Registries {
		if r.Index == "" {
			return fmt.Errorf("registries[%d].index is required", i)
		}
	}
	return nil
}

type index struct {
	url   string
	token string
}

// Plugin is the Cargo ecosystem.
type Plugin struct {
	cfg     Config
	host    sdk.Host
	http    *deps.HTTPGetter
	indexes []index
	Getenv  func(string) string
	HTTP    *http.Client
}

func init() { sdk.Register(New()) }

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: "Rust crates and workspaces: graphs from Cargo.lock, versions from sparse indexes, updates via cargo update --precise"}
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
	p.http = deps.NewHTTPGetter(h, p.HTTP, p.cfg.CacheTTL.Std())
	p.indexes = nil
	for _, r := range p.cfg.Registries {
		p.indexes = append(p.indexes, index{url: strings.TrimSuffix(strings.TrimPrefix(r.Index, "sparse+"), "/"), token: p.getenv(r.TokenEnv)})
	}
	p.indexes = append(p.indexes, index{url: CratesIO})
	return nil
}

// ToolRequirements implements sdk.ToolRequirer. Cargo is only needed without
// Cargo.lock and to realize fixes.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement { return nil }

func cargoRequirement() sdk.ToolRequirement {
	return sdk.ToolRequirement{Name: "cargo", Constraint: MinCargo, Purpose: "update Cargo.lock"}
}

// Detect implements sdk.Ecosystem: one project per workspace root or
// standalone crate.
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
		if d.Name() != "Cargo.toml" {
			return nil
		}
		data, err := fs.ReadFile(ws.FS, rel)
		if err != nil {
			return err
		}
		m, err := domain.ParseManifest(rel, data)
		if err != nil {
			// Reported when the graph is built.
			m = &domain.Manifest{Path: rel, HasPackageSection: true}
		}
		manifests[path.Dir(rel)] = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	members := map[string][]string{}
	for _, dir := range domainSorted(manifests) {
		root := dir
		for d := path.Dir(dir); dir != "."; d = path.Dir(d) {
			if m, ok := manifests[d]; ok && m.IsWorkspace {
				rel := strings.TrimPrefix(dir, d+"/")
				if d == "." {
					rel = dir
				}
				if !m.Excluded(rel) {
					root = d
				}
				break
			}
			if d == "." {
				break
			}
		}
		if !manifests[dir].HasPackageSection && !manifests[dir].IsWorkspace {
			continue
		}
		members[root] = append(members[root], dir)
	}
	join := func(dir, name string) string {
		if dir == "." {
			return name
		}
		return dir + "/" + name
	}
	var projects []sdk.Project
	for root, dirs := range members {
		sort.Strings(dirs)
		var ms []string
		for _, d := range dirs {
			ms = append(ms, join(d, "Cargo.toml"))
		}
		lock := ""
		if files[join(root, "Cargo.lock")] {
			lock = join(root, "Cargo.lock")
			ms = append(ms, lock)
		}
		for _, extra := range []string{".cargo/config.toml", ".cargo/config"} {
			if files[join(root, extra)] {
				ms = append(ms, join(root, extra))
			}
		}
		attrs := map[string]string{"lockfile": lock, "members": strings.Join(dirs, ",")}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, root), Ecosystem: ID, Dir: root, Manifests: ms, Attributes: attrs})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

func domainSorted(m map[string]*domain.Manifest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// projectManifests parses the member manifests of a project.
func projectManifests(fsys fs.FS, project sdk.Project) ([]*domain.Manifest, error) {
	var out []*domain.Manifest
	for _, dir := range strings.Split(project.Attributes["members"], ",") {
		if dir == "" {
			continue
		}
		file := path.Join(dir, "Cargo.toml")
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		m, err := domain.ParseManifest(file, data)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Cargo.toml in project %s", project.ID)
	}
	return out, nil
}

// Produce implements sdk.FactProducer.
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("cargo cannot produce %s", req.Name)
	}
	project := req.Project
	manifests, err := projectManifests(req.Workspace.FS, project)
	if err != nil {
		return nil, err
	}
	var data []byte
	source := "Cargo.lock"
	if lf := project.Attributes["lockfile"]; lf != "" {
		if data, err = fs.ReadFile(req.Workspace.FS, lf); err != nil {
			return nil, err
		}
	} else {
		if data, err = p.generateLockfile(ctx, req.Workspace, project); err != nil {
			return nil, err
		}
		source = "cargo generate-lockfile"
	}
	lock, err := domain.ParseLock(data)
	if err != nil {
		return nil, err
	}
	kinds := map[string]map[string]domain.Kind{}
	for _, m := range manifests {
		if m.Name != "" {
			kinds[m.Name] = m.Kinds()
		}
	}
	return lock.Graph("project:"+project.ID, source, kinds)
}

// generateLockfile runs `cargo generate-lockfile`, reads Cargo.lock and
// removes it again.
func (p *Plugin) generateLockfile(ctx context.Context, ws *sdk.Workspace, project sdk.Project) ([]byte, error) {
	if p.host == nil {
		return nil, errors.New("cargo plugin not initialized")
	}
	tool, err := p.host.Tools().Require(ctx, cargoRequirement(), &project)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(ws.Root, filepath.FromSlash(project.Dir))
	lock := filepath.Join(dir, "Cargo.lock")
	defer os.Remove(lock)
	if _, err := tool.Run(ctx, sdk.RunSpec{Args: []string{"generate-lockfile"}, Dir: dir, Env: cargoEnv}); err != nil {
		return nil, fmt.Errorf("cargo generate-lockfile failed: %w", err)
	}
	data, err := os.ReadFile(lock)
	if err != nil {
		return nil, fmt.Errorf("cargo did not write Cargo.lock: %w", err)
	}
	return data, nil
}

var cargoEnv = map[string]string{"CARGO_TERM_COLOR": "never", "CARGO_TERM_PROGRESS_WHEN": "never"}

// indexPath returns the sparse index path of a crate.
func indexPath(name string) string {
	n := strings.ToLower(name)
	switch len(n) {
	case 1:
		return "1/" + n
	case 2:
		return "2/" + n
	case 3:
		return "3/" + n[:1] + "/" + n
	}
	return n[:2] + "/" + n[2:4] + "/" + n
}

// Versions implements deps.VersionRegistry: the first index that knows the
// crate returns its non-yanked versions.
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.http == nil {
		return nil, errors.New("cargo plugin not initialized")
	}
	var errs []error
	for _, ix := range p.indexes {
		h := http.Header{}
		if ix.token != "" {
			h.Set("Authorization", ix.token)
		}
		data, status, err := p.http.Get(ctx, ix.url+"/"+indexPath(pu.Name), h)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("index %s: %w", ix.url, err))
			continue
		case status == http.StatusNotFound || status == http.StatusGone || status == http.StatusForbidden:
			continue
		case status != http.StatusOK:
			errs = append(errs, fmt.Errorf("index %s: HTTP %d", ix.url, status))
			continue
		}
		var out []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var e struct {
				Vers   string `json:"vers"`
				Yanked bool   `json:"yanked"`
			}
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				return nil, fmt.Errorf("index %s: invalid entry for %s: %w", ix.url, pu.Name, err)
			}
			if !e.Yanked && e.Vers != "" {
				out = append(out, e.Vers)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, deps.ErrPackageNotFound
}
