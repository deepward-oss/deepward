// Package maven is the Maven ecosystem plugin: it detects Maven reactors,
// builds their dependency graphs with maven-dependency-plugin, lists
// available versions from Maven repositories and edits POMs.
package maven

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/mavenrepo"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
	"github.com/deepward-oss/deepward/plugins/ecosystem/maven/domain"
)

// ID is the plugin ID.
const ID = "maven"

// MinMaven is the Maven version required by maven-dependency-plugin 3.8.
const MinMaven = ">=3.6.3"

// Repository configures a Maven repository; credentials come from
// environment variables so they never appear in deepward.yaml.
type Repository struct {
	ID          string `yaml:"id" json:"id"`
	URL         string `yaml:"url" json:"url"`
	UsernameEnv string `yaml:"usernameEnv" json:"usernameEnv,omitempty"`
	PasswordEnv string `yaml:"passwordEnv" json:"passwordEnv,omitempty"`
	TokenEnv    string `yaml:"tokenEnv" json:"tokenEnv,omitempty"`
}

// Config is plugins.maven.
type Config struct {
	// Repositories are queried in order for available versions (default:
	// Maven Central).
	Repositories []Repository `yaml:"repositories" json:"repositories"`
	// Settings is a settings.xml passed to mvn with -s (default:
	// $DEEPWARD_MAVEN_SETTINGS).
	Settings string `yaml:"settings" json:"settings,omitempty"`
	// CacheTTL bounds the age of cached metadata (default 6h).
	CacheTTL sdk.Duration `yaml:"cacheTTL" json:"cacheTTL,omitempty"`
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

// Plugin is the Maven ecosystem.
type Plugin struct {
	cfg    Config
	host   sdk.Host
	client *mavenrepo.Client
	poms   *domain.Resolver
	// Getenv overrides os.Getenv (tests).
	Getenv func(string) string
}

func init() { sdk.Register(New()) }

// New returns the plugin with defaults.
func New() *Plugin { return &Plugin{} }

// Info implements sdk.Plugin.
func (p *Plugin) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindEcosystem, Version: sdk.BuiltinVersion,
		Description: "Maven projects: graphs via maven-dependency-plugin, versions from Maven repositories, POM edits"}
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
	settings := h.Settings()
	ttl := p.cfg.CacheTTL.Std()
	if ttl == 0 {
		ttl = settings.CacheTTL
	}
	cache := &httpcache.Cache{Dir: settings.CacheDir, TTL: ttl, Disabled: settings.NoCache || settings.CacheDir == ""}
	p.client = &mavenrepo.Client{Repositories: repos, Cache: cache, UserAgent: "deepward"}
	p.poms = &domain.Resolver{Fetch: p.client.FetchPOM}
	return nil
}

// Addons implements sdk.AddonProvider.
func (p *Plugin) Addons() []sdk.AddonSpec {
	return []sdk.AddonSpec{{Name: "dependency-plugin", Kind: sdk.AddonMavenGoal, Default: sdk.Addon{
		Coordinates: "org.apache.maven.plugins:maven-dependency-plugin", Version: "3.8.1", Goal: "tree"}}}
}

// ToolRequirements implements sdk.ToolRequirer.
func (p *Plugin) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: "mvn", Constraint: MinMaven, Purpose: "build the dependency graph"}}
}

// Detect implements sdk.Ecosystem. Every pom.xml that is not a module of
// another detected POM is the root of a project; its modules (recursively)
// are its manifests.
func (p *Plugin) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	poms := map[string]*domain.POM{}
	var order []string
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() || path.Base(rel) != "pom.xml" {
			return nil
		}
		data, err := fs.ReadFile(ws.FS, rel)
		if err != nil {
			return err
		}
		pom, perr := domain.ParsePOM(data)
		if perr != nil {
			pom = nil
		}
		poms[rel] = pom
		order = append(order, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	modules := func(rel string) []string {
		pom := poms[rel]
		if pom == nil {
			return nil
		}
		var out []string
		for _, m := range pom.Modules {
			target := path.Join(path.Dir(rel), m)
			if !strings.HasSuffix(target, ".xml") {
				target = path.Join(target, "pom.xml")
			}
			if _, ok := poms[target]; ok && target != rel {
				out = append(out, target)
			}
		}
		return out
	}
	isModule := map[string]bool{}
	for _, rel := range order {
		for _, m := range modules(rel) {
			isModule[m] = true
		}
	}
	var projects []sdk.Project
	for _, rel := range order {
		if isModule[rel] {
			continue
		}
		manifests := []string{rel}
		seen := map[string]bool{rel: true}
		queue := []string{rel}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, m := range modules(cur) {
				if !seen[m] {
					seen[m] = true
					manifests = append(manifests, m)
					queue = append(queue, m)
				}
			}
		}
		sort.Strings(manifests[1:])
		dir := path.Dir(rel)
		pr := sdk.Project{ID: sdk.ProjectID(ID, dir), Ecosystem: ID, Dir: dir, Manifests: manifests}
		if len(manifests) > 1 {
			pr.Attributes = map[string]string{"modules": fmt.Sprint(len(manifests) - 1)}
		}
		projects = append(projects, pr)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}

// Produces implements sdk.FactProducer.
func (p *Plugin) Produces() []string { return []string{deps.FactDependencyGraph} }

// AppliesTo implements sdk.FactProducer.
func (p *Plugin) AppliesTo(pr sdk.Project) bool { return pr.Ecosystem == ID }

func (p *Plugin) addon(name string) sdk.Addon {
	if p.host != nil {
		for _, a := range p.host.Addons() {
			if a.Name == name {
				return a
			}
		}
	}
	for _, s := range p.Addons() {
		if s.Name == name {
			return s.Default
		}
	}
	return sdk.Addon{}
}

// Produce implements sdk.FactProducer: it runs maven-dependency-plugin's
// tree goal by full coordinates (the POM is not modified).
func (p *Plugin) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if req.Name != deps.FactDependencyGraph {
		return nil, fmt.Errorf("maven cannot produce %s", req.Name)
	}
	if p.host == nil {
		return nil, errors.New("maven plugin not initialized")
	}
	project := req.Project
	tool, err := p.host.Tools().Require(ctx, sdk.ToolRequirement{Name: "mvn", Constraint: MinMaven}, &project)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(req.Workspace.Root, filepath.FromSlash(project.Dir))
	// The output lives under the project's target directory so that it is
	// visible to container-based tools (the workspace is mounted at the same
	// path) and ignored by git.
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(dir, "target"), "deepward-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "tree.json")
	a := p.addon("dependency-plugin")
	goal := fmt.Sprintf("%s:%s:%s", a.Coordinates, a.Version, firstNonEmpty(a.Goal, "tree"))
	args := []string{"-B", "-q", "-Dstyle.color=never"}
	if s := firstNonEmpty(p.cfg.Settings, p.getenv("DEEPWARD_MAVEN_SETTINGS")); s != "" {
		if !filepath.IsAbs(s) {
			s = filepath.Join(req.Workspace.Root, filepath.FromSlash(s))
		}
		args = append(args, "-s", s)
	}
	args = append(args, "-f", filepath.Join(dir, "pom.xml"), goal, "-DoutputType=json", "-DoutputFile="+out, "-DappendOutput=true")
	if _, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir}); err != nil {
		return nil, fmt.Errorf("mvn dependency:tree failed: %w", err)
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("mvn wrote no dependency tree: %w", err)
	}
	defer f.Close()
	trees, err := domain.ParseTrees(f)
	if err != nil {
		return nil, err
	}
	return domain.BuildGraph("project:"+project.ID, "mvn "+goal, trees, p.modulePOMs(req.Workspace, project))
}

// modulePOMs maps "groupId:artifactId" of every reactor module to its POM.
func (p *Plugin) modulePOMs(ws *sdk.Workspace, project sdk.Project) map[string]string {
	out := map[string]string{}
	for _, m := range project.Manifests {
		data, err := fs.ReadFile(ws.FS, m)
		if err != nil {
			continue
		}
		if pom, err := domain.ParsePOM(data); err == nil && pom.ArtifactID.Text != "" {
			out[pom.EffectiveGroupID()+":"+pom.ArtifactID.Text] = m
		}
	}
	return out
}

// Versions implements deps.VersionRegistry.
func (p *Plugin) Versions(ctx context.Context, _ sdk.Project, pkg string) ([]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.client == nil {
		return nil, errors.New("maven plugin not initialized")
	}
	return p.client.Versions(ctx, strings.ReplaceAll(pu.Namespace, "/", "."), pu.Name)
}

// Dependencies implements deps.DependencyLister: it reads the published POM
// of pkg at version (with its parents and imported BOMs) from the configured
// repositories.
func (p *Plugin) Dependencies(ctx context.Context, _ sdk.Project, pkg, version string) (map[string]string, error) {
	pu, err := purl.Parse(pkg)
	if err != nil {
		return nil, err
	}
	if p.poms == nil {
		return nil, errors.New("maven plugin not initialized")
	}
	return p.poms.Dependencies(ctx, strings.ReplaceAll(pu.Namespace, "/", "."), pu.Name, version)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Realize implements sdk.RemediationRealizer: it edits POM versions in place
// (literal or ${property}) and pins transitive versions in the root POM's
// dependencyManagement.
func (p *Plugin) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	var files []domain.File
	for _, m := range req.Project.Manifests {
		if err := ctx.Err(); err != nil {
			return sdk.RealizeResult{}, err
		}
		data, err := fs.ReadFile(req.Workspace.FS, m)
		if err != nil {
			return sdk.RealizeResult{}, fmt.Errorf("read %s: %w", m, err)
		}
		pom, _ := domain.ParsePOM(data)
		files = append(files, domain.File{Path: m, Data: data, POM: pom})
	}
	if len(files) == 0 {
		return sdk.RealizeResult{}, fmt.Errorf("project %s has no POM", req.Project.ID)
	}
	var res sdk.RealizeResult
	for _, c := range req.Changes {
		pu, err := purl.Parse(c.PURL)
		if err != nil {
			res.Manual = append(res.Manual, sdk.ManualChange{Change: c, Reason: "invalid package URL"})
			continue
		}
		edits, manual := domain.Plan(files, files[0].Path, c, strings.ReplaceAll(pu.Namespace, "/", "."), pu.Name)
		if manual != nil {
			res.Manual = append(res.Manual, *manual)
			continue
		}
		res.Edits = append(res.Edits, edits...)
	}
	return res, nil
}
