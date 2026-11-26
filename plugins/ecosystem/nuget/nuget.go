// Package nuget is the NuGet ecosystem plugin: it detects .NET projects,
// builds their dependency graphs from `dotnet restore` (project.assets.json),
// lists versions from NuGet v3 feeds and edits project files and
// Directory.Packages.props.
package nuget

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
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/nuget/domain"
)

// ID is the plugin ID.
const ID = "nuget"

// MinDotnet is the minimal .NET SDK version.
const MinDotnet = ">=6"

var projectExts = map[string]bool{".csproj": true, ".fsproj": true, ".vbproj": true}

// sharedManifests are files in the project directory or its parents that
// influence restore or hold versions.
var sharedManifests = []string{"Directory.Packages.props", "Directory.Build.props", "NuGet.Config", "nuget.config", "global.json"}

// dotnetEnv disables first-run work (migrations, telemetry, banners) that
// fails or slows down CI and container runs.
var dotnetEnv = map[string]string{"DOTNET_SKIP_FIRST_TIME_EXPERIENCE": "1", "DOTNET_CLI_TELEMETRY_OPTOUT": "1", "DOTNET_NOLOGO": "1",
	"DOTNET_GENERATE_ASPNET_CERTIFICATE": "false"}

// Feed configures a NuGet v3 source for version discovery.
type Feed struct {
	ID          string `yaml:"id" json:"id"`
	URL         string `yaml:"url" json:"url"`
	UsernameEnv string `yaml:"usernameEnv" json:"usernameEnv,omitempty"`
	PasswordEnv string `yaml:"passwordEnv" json:"passwordEnv,omitempty"`
	TokenEnv    string `yaml:"tokenEnv" json:"tokenEnv,omitempty"`
}

// Config is plugins.nuget.
type Config struct {
	// Feeds are v3 service indexes (default: nuget.org).
	Feeds []Feed `yaml:"feeds" json:"feeds"`
	// ConfigFile is a nuget.config passed to dotnet restore --configfile.
	ConfigFile string       `yaml:"configFile" json:"configFile,omitempty"`
	CacheTTL   sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	for i, f := range c.Feeds {
		if f.URL == "" {
			return fmt.Errorf("feeds[%d].url is required", i)
		}
	}
	return nil
}

// Plugin is the NuGet ecosystem.
type Plugin struct {
	cfg    Config
	host   sdk.Host
	feeds  *feedClient
	Getenv func(string) string
	HTTP   *http.Client
}

func init() { sdk.Register(New()) }

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: ".NET projects: graphs via dotnet restore (project.assets.json), versions from NuGet v3 feeds, project file and CPM edits"}
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
	feeds := p.cfg.Feeds
	if len(feeds) == 0 {
		feeds = []Feed{{ID: "nuget.org", URL: NuGetOrg}}
	}
	var fs []feed
	for _, f := range feeds {
		fs = append(fs, feed{ID: firstNonEmpty(f.ID, f.URL), Index: f.URL, Username: p.getenv(f.UsernameEnv), Password: p.getenv(f.PasswordEnv), Token: p.getenv(f.TokenEnv)})
	}
	st := h.Settings()
	ttl := p.cfg.CacheTTL.Std()
	if ttl == 0 {
		ttl = st.CacheTTL
	}
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	p.feeds = &feedClient{feeds: fs, http: client, bases: map[string]string{},
		cache: &httpcache.Cache{Dir: st.CacheDir, TTL: ttl, Disabled: st.NoCache || st.CacheDir == ""}}
	return nil
}

// ToolRequirements implements sdk.ToolRequirer.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: "dotnet", Constraint: MinDotnet, Purpose: "restore packages and build the dependency graph"}}
}

// Detect implements sdk.Ecosystem: one project per directory containing
// project files.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	byDir := map[string][]string{}
	shared := map[string]bool{}
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		base := path.Base(rel)
		if projectExts[strings.ToLower(path.Ext(base))] {
			byDir[path.Dir(rel)] = append(byDir[path.Dir(rel)], rel)
		}
		for _, s := range sharedManifests {
			if base == s {
				shared[rel] = true
			}
		}
		if base == "packages.lock.json" {
			shared[rel] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var projects []sdk.Project
	for dir, files := range byDir {
		sort.Strings(files)
		manifests := append([]string{}, files...)
		// Shared files in the directory and its parents.
		for d := dir; ; d = path.Dir(d) {
			for _, s := range append(append([]string{}, sharedManifests...), "packages.lock.json") {
				f := path.Join(d, s)
				if d == "." {
					f = s
				}
				if shared[f] && (s != "packages.lock.json" || d == dir) {
					manifests = append(manifests, f)
				}
			}
			if d == "." || d == "/" {
				break
			}
		}
		attrs := map[string]string{"projectFiles": strings.Join(files, ",")}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, dir), Ecosystem: ID, Dir: dir, Manifests: manifests, Attributes: attrs})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// Produce implements sdk.FactProducer: dotnet restore, then parse
// obj/project.assets.json of every project file in the directory.
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("nuget cannot produce %s", req.Name)
	}
	if p.host == nil {
		return nil, errors.New("nuget plugin not initialized")
	}
	project := req.Project
	tool, err := p.host.Tools().Require(ctx, sdk.ToolRequirement{Name: "dotnet", Constraint: MinDotnet}, &project)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(req.Workspace.Root, filepath.FromSlash(project.Dir))
	files := project.Manifests
	if pf := project.Attributes["projectFiles"]; pf != "" {
		files = strings.Split(pf, ",")
	}
	var all []*domain.Assets
	for _, rel := range files {
		if !projectExts[strings.ToLower(path.Ext(rel))] {
			continue
		}
		file := filepath.Join(req.Workspace.Root, filepath.FromSlash(rel))
		args := []string{"restore", file, "--nologo"}
		if cf := p.cfg.ConfigFile; cf != "" {
			if !filepath.IsAbs(cf) {
				cf = filepath.Join(req.Workspace.Root, filepath.FromSlash(cf))
			}
			args = append(args, "--configfile", cf)
		}
		if _, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir, Env: dotnetEnv}); err != nil {
			return nil, fmt.Errorf("dotnet restore %s failed: %w", rel, err)
		}
		assetsPath := filepath.Join(filepath.Dir(file), "obj", "project.assets.json")
		if _, err := os.Stat(assetsPath); err != nil {
			// Custom intermediate paths: ask MSBuild (SDK 8+).
			res, perr := tool.Run(ctx, sdk.RunSpec{Args: []string{"msbuild", file, "-getProperty:ProjectAssetsFile", "-nologo"}, Dir: dir})
			if perr != nil || strings.TrimSpace(string(res.Stdout)) == "" {
				return nil, fmt.Errorf("project.assets.json not found for %s", rel)
			}
			assetsPath = strings.TrimSpace(string(res.Stdout))
		}
		f, err := os.Open(assetsPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", assetsPath, err)
		}
		a, err := domain.ParseAssets(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, a)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no .NET project files in %s", project.Dir)
	}
	return domain.BuildGraph("project:"+project.ID, "dotnet restore (project.assets.json)", all)
}

// Versions implements deps.VersionRegistry.
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.feeds == nil {
		return nil, errors.New("nuget plugin not initialized")
	}
	return p.feeds.Versions(ctx, pu.Name)
}

// Realize implements sdk.RemediationRealizer: it edits PackageReference
// versions (attribute, child element or $(Property)), PackageVersion entries
// of Central Package Management, and pins transitive packages.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	var files []domain.File
	var projectFiles []string
	props := ""
	for _, m := range req.Project.Manifests {
		base := path.Base(m)
		isProject := projectExts[strings.ToLower(path.Ext(m))]
		if !isProject && base != "Directory.Packages.props" && base != "Directory.Build.props" {
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
		if isProject {
			projectFiles = append(projectFiles, m)
		}
		if base == "Directory.Packages.props" && props == "" {
			props = m
		}
	}
	var res sdk.RealizeResult
	for _, c := range req.Changes {
		pu, err := purl.Parse(c.PURL)
		if err != nil {
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
			continue
		}
		edits, manual := domain.Plan(files, projectFiles, props, c, pu.Name)
		if manual != nil {
			res.Manual = append(res.Manual, *manual)
			continue
		}
		res.Edits = append(res.Edits, edits...)
	}
	return res, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
