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

// Detect implements sdk.Ecosystem. Each project file belongs to one
// project, bounded by the nearest Directory.Packages.props at or above it
// (the Central Package Management root), else by the nearest solution
// (.sln, .slnx) at or above it that lists it, else by its own directory.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var projectFiles, solutions []string
	cpmDirs := map[string]bool{}
	shared := map[string]bool{}
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		base := path.Base(rel)
		lower := strings.ToLower(base)
		switch {
		case domain.IsProjectFile(rel):
			projectFiles = append(projectFiles, rel)
		case strings.HasSuffix(lower, ".sln") || strings.HasSuffix(lower, ".slnx"):
			solutions = append(solutions, rel)
		}
		if base == "Directory.Packages.props" {
			cpmDirs[path.Dir(rel)] = true
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
	sort.Strings(solutions)
	listedBy := map[string][]string{} // project file -> solutions listing it
	listed := map[string][]string{}   // solution -> project files it lists
	for _, sol := range solutions {
		data, err := fs.ReadFile(ws.FS, sol)
		if err != nil {
			continue
		}
		var rels []string
		if strings.HasSuffix(strings.ToLower(sol), ".slnx") {
			rels, _ = domain.SlnxProjects(data)
		} else {
			rels = domain.SolutionProjects(data)
		}
		for _, r := range rels {
			if pf, ok := domain.Resolve(path.Dir(sol), r); ok {
				listedBy[pf] = append(listedBy[pf], sol)
				listed[sol] = append(listed[sol], pf)
			}
		}
	}
	type group struct {
		dir, boundary string
		members       []string
	}
	groups := map[string]*group{}
	for _, pf := range projectFiles {
		dir := path.Dir(pf)
		key, g := "", &group{dir: dir}
		if cpm, ok := nearestAncestor(dir, cpmDirs); ok {
			key, g.dir, g.boundary = "cpm:"+cpm, cpm, join(cpm, "Directory.Packages.props")
		} else if sol, ok := nearestSolution(dir, listedBy[pf]); ok {
			key, g.dir, g.boundary = "sln:"+sol, path.Dir(sol), sol
		} else {
			key = "dir:" + dir
		}
		if existing, ok := groups[key]; ok {
			g = existing
		} else {
			groups[key] = g
		}
		g.members = append(g.members, pf)
	}
	var projects []sdk.Project
	for _, g := range groups {
		sort.Strings(g.members)
		members := map[string]bool{}
		for _, m := range g.members {
			members[m] = true
		}
		manifests := append([]string{}, g.members...)
		if g.boundary != "" && !strings.HasSuffix(g.boundary, "Directory.Packages.props") {
			manifests = append(manifests, g.boundary)
		}
		for _, m := range g.members {
			if lock := join(path.Dir(m), "packages.lock.json"); shared[lock] {
				manifests = append(manifests, lock)
			}
		}
		// Shared files in the project directory and its parents.
		for d := g.dir; ; d = path.Dir(d) {
			for _, s := range sharedManifests {
				if f := join(d, s); shared[f] {
					manifests = append(manifests, f)
				}
			}
			if d == "." || d == "/" {
				break
			}
		}
		attrs := map[string]string{"projectFiles": strings.Join(g.members, ",")}
		// Solutions to restore: those whose projects are all members.
		var restore []string
		for _, sol := range solutions {
			all := len(listed[sol]) > 0
			for _, pf := range listed[sol] {
				all = all && members[pf]
			}
			if all && within(sol, g.dir) {
				restore = append(restore, sol)
			}
		}
		if len(restore) > 0 {
			attrs["solutions"] = strings.Join(restore, ",")
		}
		projects = append(projects, sdk.Project{ID: sdk.ProjectID(ID, g.dir), Ecosystem: ID, Dir: g.dir, Manifests: dedupe(manifests), Attributes: attrs})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

// join is path.Join that keeps "x" for directory ".".
func join(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// within reports whether rel is in dir or below it.
func within(rel, dir string) bool {
	return dir == "." || rel == dir || strings.HasPrefix(rel, dir+"/")
}

// nearestAncestor returns the deepest directory of dirs that is dir or one
// of its parents.
func nearestAncestor(dir string, dirs map[string]bool) (string, bool) {
	for d := dir; ; d = path.Dir(d) {
		if dirs[d] {
			return d, true
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

// nearestSolution picks, among the solutions listing a project file, the
// one in the deepest directory at or above dir (the first by path on ties).
func nearestSolution(dir string, sols []string) (string, bool) {
	best, depth := "", -1
	for _, sol := range sols {
		sd := path.Dir(sol)
		if !within(dir, sd) {
			continue
		}
		d := 0
		if sd != "." {
			d = strings.Count(sd, "/") + 1
		}
		if d > depth || (d == depth && sol < best) {
			best, depth = sol, d
		}
	}
	return best, depth >= 0
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

// Produce implements sdk.FactProducer: one dotnet restore per solution of
// the project (and per member no other member references), then the
// obj/project.assets.json of every member project file.
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
	members := memberFiles(project)
	if len(members) == 0 {
		return nil, fmt.Errorf("no .NET project files in %s", project.Dir)
	}
	restore := func(target string) error {
		args := []string{"restore", filepath.Join(req.Workspace.Root, filepath.FromSlash(target)), "--nologo"}
		if cf := p.cfg.ConfigFile; cf != "" {
			if !filepath.IsAbs(cf) {
				cf = filepath.Join(req.Workspace.Root, filepath.FromSlash(cf))
			}
			args = append(args, "--configfile", cf)
		}
		if _, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir, Env: dotnetEnv}); err != nil {
			return fmt.Errorf("dotnet restore %s failed: %w", target, err)
		}
		return nil
	}
	for _, target := range restoreTargets(req.Workspace, project, members) {
		if err := restore(target); err != nil {
			return nil, err
		}
	}
	var all []domain.OwnedAssets
	for _, rel := range members {
		file := filepath.Join(req.Workspace.Root, filepath.FromSlash(rel))
		assetsPath, ok := assetsFile(ctx, tool, file, dir)
		if !ok {
			// A solution restore skips projects its active configuration does
			// not build: restore such a project file on its own.
			if err := restore(rel); err != nil {
				return nil, err
			}
			if assetsPath, ok = assetsFile(ctx, tool, file, dir); !ok {
				return nil, fmt.Errorf("project.assets.json not found for %s", rel)
			}
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
		all = append(all, domain.OwnedAssets{Path: rel, Assets: a})
	}
	return domain.BuildGraph("project:"+project.ID, "dotnet restore (project.assets.json)", all)
}

// assetsFile finds the assets file of a restored project file: next to it
// in obj/, else where MSBuild says (custom intermediate paths, SDK 8+).
func assetsFile(ctx context.Context, tool sdk.Tool, file, dir string) (string, bool) {
	assetsPath := filepath.Join(filepath.Dir(file), "obj", "project.assets.json")
	if _, err := os.Stat(assetsPath); err == nil {
		return assetsPath, true
	}
	res, err := tool.Run(ctx, sdk.RunSpec{Args: []string{"msbuild", file, "-getProperty:ProjectAssetsFile", "-nologo"}, Dir: dir})
	if err != nil {
		return "", false
	}
	custom := strings.TrimSpace(string(res.Stdout))
	if custom == "" {
		return "", false
	}
	if _, err := os.Stat(custom); err != nil {
		return "", false
	}
	return custom, true
}

// memberFiles lists the project files of a project.
func memberFiles(project sdk.Project) []string {
	files := project.Manifests
	if pf := project.Attributes["projectFiles"]; pf != "" {
		files = strings.Split(pf, ",")
	}
	var out []string
	for _, f := range files {
		if domain.IsProjectFile(f) {
			out = append(out, f)
		}
	}
	return out
}

// restoreTargets returns what to pass to dotnet restore: the project's
// solutions, then each member that no solution lists and no other member
// references (restoring a project file restores what it references). If
// references form a cycle with nothing left to start from, every member is
// restored.
func restoreTargets(ws *sdk.Workspace, project sdk.Project, members []string) []string {
	var targets []string
	covered := map[string]bool{}
	for _, sol := range strings.Split(project.Attributes["solutions"], ",") {
		if sol == "" {
			continue
		}
		data, err := fs.ReadFile(ws.FS, sol)
		if err != nil {
			continue
		}
		var rels []string
		if strings.HasSuffix(strings.ToLower(sol), ".slnx") {
			rels, _ = domain.SlnxProjects(data)
		} else {
			rels = domain.SolutionProjects(data)
		}
		targets = append(targets, sol)
		for _, r := range rels {
			if pf, ok := domain.Resolve(path.Dir(sol), r); ok {
				covered[pf] = true
			}
		}
	}
	referenced := map[string]bool{}
	for _, m := range members {
		data, err := fs.ReadFile(ws.FS, m)
		if err != nil {
			continue
		}
		refs, _ := domain.ProjectReferences(data)
		for _, r := range refs {
			if pf, ok := domain.Resolve(path.Dir(m), r); ok {
				referenced[pf] = true
			}
		}
	}
	var rest []string
	for _, m := range members {
		if !covered[m] && !referenced[m] {
			rest = append(rest, m)
		}
	}
	if len(targets) == 0 && len(rest) == 0 {
		return members
	}
	return append(targets, rest...)
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
