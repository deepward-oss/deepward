// Package sbt is the sbt ecosystem plugin: it detects sbt builds, builds
// their dependency graphs with an sbt plugin injected into the meta-build
// (no project files are written), lists available versions from Maven
// repositories and edits Scala build files.
package sbt

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/mavenrepo"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/sbt/domain"
)

// ID is the plugin ID.
const ID = "sbt"

// MinSbt is the minimal sbt launcher version (--addPluginSbtFile needs 1.4).
const MinSbt = ">=1.4"

//go:embed assets/DeepwardGraphPlugin.scala
var graphPlugin []byte

// Repository is a Maven-compatible repository for version discovery.
type Repository struct {
	ID          string `yaml:"id" json:"id"`
	URL         string `yaml:"url" json:"url"`
	UsernameEnv string `yaml:"usernameEnv" json:"usernameEnv,omitempty"`
	PasswordEnv string `yaml:"passwordEnv" json:"passwordEnv,omitempty"`
	TokenEnv    string `yaml:"tokenEnv" json:"tokenEnv,omitempty"`
}

// Config is plugins.sbt.
type Config struct {
	Repositories []Repository `yaml:"repositories" json:"repositories"`
	// CrossBuild runs the graph task for every crossScalaVersion (default true).
	CrossBuild *bool        `yaml:"crossBuild" json:"crossBuild,omitempty"`
	CacheTTL   sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, r := range c.Repositories {
		if r.URL == "" {
			return fmt.Errorf("repositories[%d].url is required", i)
		}
	}
	return nil
}

// Plugin is the sbt ecosystem.
type Plugin struct {
	cfg    Config
	host   sdk.Host
	client *mavenrepo.Client
	Getenv func(string) string
}

func init() { sdk.Register(New()) }

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: "sbt builds: graphs via an injected sbt plugin (updateFull), versions from Maven repositories, Scala build edits"}
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
	var repos []mavenrepo.Repository
	for _, r := range p.cfg.Repositories {
		repos = append(repos, mavenrepo.Repository{ID: firstNonEmpty(r.ID, r.URL), URL: r.URL,
			Username: p.getenv(r.UsernameEnv), Password: p.getenv(r.PasswordEnv), Token: p.getenv(r.TokenEnv)})
	}
	st := h.Settings()
	ttl := p.cfg.CacheTTL.Std()
	if ttl == 0 {
		ttl = st.CacheTTL
	}
	p.client = &mavenrepo.Client{Repositories: repos, UserAgent: "deepward",
		Cache: &httpcache.Cache{Dir: st.CacheDir, TTL: ttl, Disabled: st.NoCache || st.CacheDir == ""}}
	return nil
}

// Addons implements sdk.AddonProvider.
func (p *Plugin) Addons() []sdk.AddonSpec {
	return []sdk.AddonSpec{{Name: "graph-dump", Kind: sdk.AddonSbtPluginFile, Default: sdk.Addon{File: "(built-in DeepwardGraphPlugin.scala)", Builtin: true}}}
}

// ToolRequirements implements sdk.ToolRequirer.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: "sbt", Constraint: MinSbt, Purpose: "build the dependency graph"}}
}

// Detect implements sdk.Ecosystem. A directory with build.sbt (or
// project/build.properties) is the root of a build unless it is inside
// another build's directory tree.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidates := map[string]bool{}
	files := map[string][]string{}
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		dir, base := path.Dir(rel), path.Base(rel)
		switch {
		case base == "build.sbt":
			candidates[dir] = true
			files[dir] = append(files[dir], rel)
		case path.Base(dir) == "project" && (base == "build.properties" || strings.HasSuffix(base, ".sbt") || strings.HasSuffix(base, ".scala")):
			owner := path.Dir(dir)
			if base == "build.properties" {
				candidates[owner] = true
			}
			files[owner] = append(files[owner], rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(candidates))
	for d := range candidates {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var roots []string
	for _, d := range dirs {
		nested := false
		for _, r := range roots {
			if r == "." || strings.HasPrefix(d, r+"/") {
				nested = true
				break
			}
		}
		if !nested {
			roots = append(roots, d)
		}
	}
	var projects []sdk.Project
	for _, r := range roots {
		var manifests []string
		for d, fs := range files {
			if d == r || r == "." || strings.HasPrefix(d, r+"/") {
				manifests = append(manifests, fs...)
			}
		}
		sort.Slice(manifests, func(i, j int) bool {
			// build.sbt of the root first.
			ri, rj := manifests[i] == path.Join(r, "build.sbt"), manifests[j] == path.Join(r, "build.sbt")
			if ri != rj {
				return ri
			}
			return manifests[i] < manifests[j]
		})
		if len(manifests) == 0 {
			continue
		}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, r), Ecosystem: ID, Dir: r, Manifests: manifests})
	}
	return projects, nil
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// Produce implements sdk.FactProducer.
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("sbt cannot produce %s", req.Name)
	}
	if p.host == nil {
		return nil, errors.New("sbt plugin not initialized")
	}
	project := req.Project
	tool, err := p.host.Tools().Require(ctx, sdk.ToolRequirement{Name: "sbt", Constraint: MinSbt}, &project)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(req.Workspace.Root, filepath.FromSlash(project.Dir))
	// The temporary files live under the build's target directory so that
	// they are visible to container-based tools (the workspace is mounted
	// at the same path) and ignored by git.
	tmp := filepath.Join(dir, "target", "deepward")
	if err := os.MkdirAll(filepath.Join(tmp, "src"), 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "src", "DeepwardGraphPlugin.scala"), graphPlugin, 0o644); err != nil {
		return nil, err
	}
	pluginFile := filepath.Join(tmp, "deepward.sbt")
	src := filepath.ToSlash(filepath.Join(tmp, "src"))
	if err := os.WriteFile(pluginFile, []byte("Compile / unmanagedSourceDirectories += file("+strconv.Quote(src)+")\n"), 0o644); err != nil {
		return nil, err
	}
	out := filepath.Join(tmp, "graph.jsonl")
	args := []string{"-batch", "-Dsbt.supershell=false", "-Dsbt.color=false", "-Dsbt.server.forcestart=false", "-Ddeepward.out=" + out,
		"--addPluginSbtFile=" + pluginFile}
	for _, a := range p.host.Addons() {
		if a.Kind == sdk.AddonSbtPluginFile && !a.Builtin && a.File != "" {
			f := a.File
			if !filepath.IsAbs(f) {
				f = filepath.Join(req.Workspace.Root, filepath.FromSlash(f))
			}
			args = append(args, "--addPluginSbtFile="+f)
		}
	}
	task := "+deepwardGraph"
	if p.cfg.CrossBuild != nil && !*p.cfg.CrossBuild {
		task = "deepwardGraph"
	}
	args = append(args, task)
	if _, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir}); err != nil {
		return nil, fmt.Errorf("sbt %s failed: %w", task, err)
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("sbt wrote no dependency graph: %w", err)
	}
	defer f.Close()
	reports, err := domain.ParseReports(f)
	if err != nil {
		return nil, err
	}
	return domain.BuildGraph("project:"+project.ID, "sbt updateFull (Deepward graph plugin)", reports)
}

// Versions implements deps.VersionRegistry. Scala artifacts keep their
// binary suffix in the purl name, so only versions of the same suffix are
// returned (cats-core_2.13 never yields cats-core_3).
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.client == nil {
		return nil, errors.New("sbt plugin not initialized")
	}
	return p.client.Versions(ctx, strings.ReplaceAll(pu.Namespace, "/", "."), pu.Name)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Realize implements sdk.RemediationRealizer: it edits version literals and
// shared string vals in build.sbt and project/*.scala files.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	var files []domain.File
	for _, m := range req.Project.Manifests {
		if !strings.HasSuffix(m, ".sbt") && !strings.HasSuffix(m, ".scala") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return sdk.RealizeResult{}, err
		}
		data, err := fs.ReadFile(req.Workspace.FS, m)
		if err != nil {
			return sdk.RealizeResult{}, fmt.Errorf("read %s: %w", m, err)
		}
		files = append(files, domain.File{Path: m, Data: data})
	}
	var res sdk.RealizeResult
	for _, c := range req.Changes {
		pu, err := purl.Parse(c.PURL)
		if err != nil {
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
			continue
		}
		edits, manual := domain.Plan(files, c, strings.ReplaceAll(pu.Namespace, "/", "."), pu.Name)
		if manual != nil {
			res.Manual = append(res.Manual, *manual)
			continue
		}
		res.Edits = append(res.Edits, edits...)
	}
	return res, nil
}
