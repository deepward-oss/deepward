package sdktest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sarif"
)

func info(id string, kind sdk.Kind) sdk.Info {
	return sdk.Info{ID: id, Kind: kind, Version: "0.0.0-test", Description: "fake " + string(kind) + " for tests"}
}

// FakeEcosystem detects one project per directory containing Marker.
type FakeEcosystem struct {
	ID     string
	Marker string
	// Facts maps fact name to a producer; when set, the ecosystem acts as a
	// FactProducer for its projects.
	Facts map[string]func(ctx context.Context, req sdk.FactRequest) (any, error)
	// RealizeFunc, when set, makes the ecosystem a RemediationRealizer.
	RealizeFunc func(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error)
	AddonSpecs  []sdk.AddonSpec
	Tools       []sdk.ToolDefinition
	Calls       atomic.Int64
}

// Info implements sdk.Plugin.
func (e *FakeEcosystem) Info() sdk.Info { return info(e.ID, sdk.KindEcosystem) }

// Detect implements sdk.Ecosystem.
func (e *FakeEcosystem) Detect(ctx context.Context, ws *sdk.Workspace) ([]sdk.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []sdk.Project
	walkErr := ws.Walk(func(rel string, d fs.DirEntry) error {
		if d.IsDir() || path.Base(rel) != e.Marker {
			return nil
		}
		dir := path.Dir(rel)
		out = append(out, sdk.Project{ID: sdk.ProjectID(e.ID, dir), Ecosystem: e.ID, Dir: dir, Manifests: []string{rel}})
		return nil
	})
	return out, walkErr
}

// Produces implements sdk.FactProducer when Facts is set.
func (e *FakeEcosystem) Produces() []string {
	names := make([]string, 0, len(e.Facts))
	for n := range e.Facts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AppliesTo implements sdk.FactProducer.
func (e *FakeEcosystem) AppliesTo(p sdk.Project) bool { return p.Ecosystem == e.ID }

// Produce implements sdk.FactProducer.
func (e *FakeEcosystem) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	e.Calls.Add(1)
	fn, ok := e.Facts[req.Name]
	if !ok {
		return nil, fmt.Errorf("fake ecosystem %s cannot produce %s", e.ID, req.Name)
	}
	return fn(ctx, req)
}

// Realize implements sdk.RemediationRealizer.
func (e *FakeEcosystem) Realize(ctx context.Context, req sdk.RealizeRequest) (sdk.RealizeResult, error) {
	if e.RealizeFunc == nil {
		return sdk.RealizeResult{}, errors.New("not supported")
	}
	return e.RealizeFunc(ctx, req)
}

// Addons implements sdk.AddonProvider.
func (e *FakeEcosystem) Addons() []sdk.AddonSpec { return e.AddonSpecs }

// ToolDefinitions implements sdk.ToolDefiner.
func (e *FakeEcosystem) ToolDefinitions() []sdk.ToolDefinition { return e.Tools }

// FakeAnalyzer returns findings computed by Func.
type FakeAnalyzer struct {
	ID       string
	Needs    []string
	Scoped   sdk.AnalyzerScope
	Func     func(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error)
	Settings *FakeSettings
	Calls    atomic.Int64
}

// FakeSettings is the configuration of FakeAnalyzer.
type FakeSettings struct {
	Threshold int          `yaml:"threshold" json:"threshold"`
	Timeout   sdk.Duration `yaml:"timeout" json:"timeout"`
	Tags      []string     `yaml:"tags" json:"tags"`
}

// Validate implements sdk.Validator.
func (s *FakeSettings) Validate() error {
	if s.Threshold < 0 {
		return errors.New("threshold must be >= 0")
	}
	return nil
}

// Info implements sdk.Plugin.
func (a *FakeAnalyzer) Info() sdk.Info { return info(a.ID, sdk.KindAnalyzer) }

// Scope implements sdk.Analyzer.
func (a *FakeAnalyzer) Scope() sdk.AnalyzerScope {
	if a.Scoped == "" {
		return sdk.ScopeProject
	}
	return a.Scoped
}

// Requires implements sdk.Analyzer.
func (a *FakeAnalyzer) Requires() []string { return a.Needs }

// Analyze implements sdk.Analyzer.
func (a *FakeAnalyzer) Analyze(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
	a.Calls.Add(1)
	if a.Func == nil {
		return nil, nil
	}
	return a.Func(ctx, req)
}

// ConfigTarget implements sdk.Configurable when Settings is set.
func (a *FakeAnalyzer) ConfigTarget() any {
	if a.Settings == nil {
		a.Settings = &FakeSettings{}
	}
	return a.Settings
}

// SimpleFinding builds a valid finding for tests.
func SimpleFinding(analyzer, rule string, sev sdk.Severity, project string) sdk.Finding {
	return sdk.Finding{
		Analyzer: analyzer, Rule: rule, Kind: sdk.KindCodeIssue, Severity: sev, Project: project,
		Message:   rule + " found",
		Locations: []sdk.Location{{File: &sdk.FileLocation{Path: "src/main.go", StartLine: 1}}},
	}
}

// FakeFactProducer produces a fact via Func.
type FakeFactProducer struct {
	ID      string
	Names   []string
	Applies func(sdk.Project) bool
	Func    func(ctx context.Context, req sdk.FactRequest) (any, error)
	Calls   atomic.Int64
}

// Info implements sdk.Plugin.
func (p *FakeFactProducer) Info() sdk.Info { return info(p.ID, sdk.KindFactProducer) }

// Produces implements sdk.FactProducer.
func (p *FakeFactProducer) Produces() []string { return p.Names }

// AppliesTo implements sdk.FactProducer.
func (p *FakeFactProducer) AppliesTo(pr sdk.Project) bool {
	return p.Applies == nil || p.Applies(pr)
}

// Produce implements sdk.FactProducer.
func (p *FakeFactProducer) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	p.Calls.Add(1)
	return p.Func(ctx, req)
}

// FakeReporter renders "<format>: N findings" lines.
type FakeReporter struct {
	ID   string
	Name string
}

// Info implements sdk.Plugin.
func (r *FakeReporter) Info() sdk.Info { return info(r.ID, sdk.KindReporter) }

// Format implements sdk.Reporter.
func (r *FakeReporter) Format() string { return r.Name }

// FileName implements sdk.Reporter.
func (r *FakeReporter) FileName() string { return "report." + r.Name }

// Render implements sdk.Reporter.
func (r *FakeReporter) Render(_ context.Context, res *sdk.ScanResult, w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %d findings\n", r.Name, len(res.Findings))
	return err
}

// FakeOutputFormat parses SARIF or a JSON fact.
type FakeOutputFormat struct {
	ID       string
	Format   string
	FactName string
}

// Info implements sdk.Plugin.
func (f *FakeOutputFormat) Info() sdk.Info { return info(f.ID, sdk.KindOutputFormat) }

// Name implements sdk.OutputFormat.
func (f *FakeOutputFormat) Name() string { return f.Format }

// Produces implements sdk.OutputFormat.
func (f *FakeOutputFormat) Produces() []string {
	if f.FactName != "" {
		return []string{f.FactName}
	}
	return []string{sdk.OutputFindings}
}

// Parse implements sdk.OutputFormat.
func (f *FakeOutputFormat) Parse(_ context.Context, r io.Reader, req sdk.ParseRequest) (sdk.ParseResult, error) {
	if f.FactName != "" {
		var v map[string]any
		if err := json.NewDecoder(r).Decode(&v); err != nil {
			return sdk.ParseResult{}, err
		}
		return sdk.ParseResult{Fact: v}, nil
	}
	log, err := sarif.Decode(r)
	if err != nil {
		return sdk.ParseResult{}, err
	}
	fs, err := sarif.ToFindings(log, req.Integration, req.Tool)
	return sdk.ParseResult{Findings: fs}, err
}

// FakeToolResolver resolves tools from a map of name to command.
type FakeToolResolver struct {
	ID       string
	Commands map[string][]string
}

// Info implements sdk.Plugin.
func (r *FakeToolResolver) Info() sdk.Info { return info(r.ID, sdk.KindToolResolver) }

// Resolve implements sdk.ToolResolver.
func (r *FakeToolResolver) Resolve(_ context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	cmd, ok := r.Commands[req.Definition.Name]
	if !ok {
		return nil, nil
	}
	return &sdk.ToolCandidate{Command: cmd, Source: "fake:" + req.Definition.Name}, nil
}

// FakeTool is a scripted tool.
type FakeTool struct {
	ToolName    string
	ToolVersion string
	RunFunc     func(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error)
	mu          sync.Mutex
	Invocations []sdk.RunSpec
}

// Name implements sdk.Tool.
func (t *FakeTool) Name() string { return t.ToolName }

// Version implements sdk.Tool.
func (t *FakeTool) Version() string { return t.ToolVersion }

// Resolver implements sdk.Tool.
func (t *FakeTool) Resolver() string { return "fake" }

// Command implements sdk.Tool.
func (t *FakeTool) Command() []string { return []string{t.ToolName} }

// Run implements sdk.Tool.
func (t *FakeTool) Run(ctx context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
	t.mu.Lock()
	t.Invocations = append(t.Invocations, spec)
	t.mu.Unlock()
	if t.RunFunc == nil {
		return sdk.RunResult{}, nil
	}
	return t.RunFunc(ctx, spec)
}

// FakeTools serves FakeTool instances by name.
type FakeTools struct {
	Tools map[string]*FakeTool
}

// Require implements sdk.Tools.
func (f *FakeTools) Require(_ context.Context, req sdk.ToolRequirement, _ *sdk.Project) (sdk.Tool, error) {
	t, ok := f.Tools[req.Name]
	if !ok {
		return nil, &sdk.ToolError{Tool: req.Name, Reason: sdk.ErrToolNotFound}
	}
	return t, nil
}

// FakeHost is a minimal sdk.Host.
type FakeHost struct {
	PluginsByKind map[sdk.Kind][]sdk.Plugin
	ToolSet       sdk.Tools
	Run           sdk.RunSettings
	AddonList     []sdk.Addon
	ClockValue    sdk.Clock
}

// Plugins implements sdk.Host.
func (h *FakeHost) Plugins(kind sdk.Kind) []sdk.Plugin { return h.PluginsByKind[kind] }

// Tools implements sdk.Host.
func (h *FakeHost) Tools() sdk.Tools {
	if h.ToolSet == nil {
		return &FakeTools{}
	}
	return h.ToolSet
}

// Logger implements sdk.Host.
func (h *FakeHost) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Clock implements sdk.Host.
func (h *FakeHost) Clock() sdk.Clock {
	if h.ClockValue == nil {
		return sdk.SystemClock{}
	}
	return h.ClockValue
}

// Settings implements sdk.Host.
func (h *FakeHost) Settings() sdk.RunSettings { return h.Run }

// Addons implements sdk.Host.
func (h *FakeHost) Addons() []sdk.Addon { return h.AddonList }

// FakePlatform is an in-memory git hosting service.
type FakePlatform struct {
	ID     string
	Domain string
	Repo   *FakeRepo
}

// Info implements sdk.Plugin.
func (p *FakePlatform) Info() sdk.Info { return info(p.ID, sdk.KindPlatform) }

// Detect implements sdk.Platform.
func (p *FakePlatform) Detect(url string) bool {
	return p.Domain != "" && strings.Contains(url, p.Domain)
}

// Connect implements sdk.Platform.
func (p *FakePlatform) Connect(_ context.Context, cfg sdk.PlatformConfig) (sdk.PlatformClient, error) {
	if cfg.Token == "" {
		return nil, errors.New("token required")
	}
	if p.Repo == nil {
		p.Repo = NewFakeRepo("main")
	}
	return p.Repo, nil
}

// FakeRepo is an in-memory repository implementing sdk.PlatformClient and
// recording write calls.
type FakeRepo struct {
	mu       sync.Mutex
	Default  string
	Files    map[string]map[string][]byte // branch -> path -> content
	Commits  map[string][]sdk.CommitInfo  // branch -> commits
	CRs      []sdk.ChangeRequest
	Comments map[string][]string
	Writes   []string
	next     int
}

// NewFakeRepo returns an empty repository.
func NewFakeRepo(defaultBranch string) *FakeRepo {
	return &FakeRepo{Default: defaultBranch, Files: map[string]map[string][]byte{defaultBranch: {}},
		Commits: map[string][]sdk.CommitInfo{}, Comments: map[string][]string{}}
}

// DefaultBranch implements sdk.PlatformClient.
func (r *FakeRepo) DefaultBranch(context.Context) (string, error) { return r.Default, nil }

// ReadFile implements sdk.PlatformClient.
func (r *FakeRepo) ReadFile(_ context.Context, ref, path string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.Files[ref][path]
	return b, ok, nil
}

// ListChangeRequests implements sdk.PlatformClient.
func (r *FakeRepo) ListChangeRequests(_ context.Context, prefix string) ([]sdk.ChangeRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sdk.ChangeRequest
	for _, cr := range r.CRs {
		if cr.State == "open" && strings.HasPrefix(cr.SourceBranch, prefix) {
			out = append(out, cr)
		}
	}
	return out, nil
}

// BranchCommits implements sdk.PlatformClient.
func (r *FakeRepo) BranchCommits(_ context.Context, branch, _ string) ([]sdk.CommitInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdk.CommitInfo{}, r.Commits[branch]...), nil
}

// Commit implements sdk.PlatformClient.
func (r *FakeRepo) Commit(_ context.Context, req sdk.CommitRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.Files[req.Branch]; exists && !req.Force {
		return "", fmt.Errorf("branch %s exists", req.Branch)
	}
	base := r.Files[req.BaseBranch]
	files := map[string][]byte{}
	for k, v := range base {
		files[k] = v
	}
	for _, f := range req.Files {
		if f.Delete {
			delete(files, f.Path)
		} else {
			files[f.Path] = f.Content
		}
	}
	r.Files[req.Branch] = files
	r.next++
	sha := "sha" + strconv.Itoa(r.next)
	r.Commits[req.Branch] = []sdk.CommitInfo{{SHA: sha, AuthorName: sdk.BotAuthor.Name, IsBot: true}}
	r.Writes = append(r.Writes, "commit:"+req.Branch)
	return sha, nil
}

// CreateChangeRequest implements sdk.PlatformClient.
func (r *FakeRepo) CreateChangeRequest(_ context.Context, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	cr := sdk.ChangeRequest{ID: strconv.Itoa(r.next), URL: "https://fake/cr/" + strconv.Itoa(r.next), Title: spec.Title, Body: spec.Body,
		SourceBranch: spec.SourceBranch, TargetBranch: spec.TargetBranch, Labels: spec.Labels, State: "open"}
	r.CRs = append(r.CRs, cr)
	r.Writes = append(r.Writes, "create:"+spec.SourceBranch)
	return cr, nil
}

// UpdateChangeRequest implements sdk.PlatformClient.
func (r *FakeRepo) UpdateChangeRequest(_ context.Context, id string, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.CRs {
		if r.CRs[i].ID == id {
			r.CRs[i].Title, r.CRs[i].Body, r.CRs[i].Labels = spec.Title, spec.Body, spec.Labels
			r.Writes = append(r.Writes, "update:"+id)
			return r.CRs[i], nil
		}
	}
	return sdk.ChangeRequest{}, fmt.Errorf("no change request %s", id)
}

// CloseChangeRequest implements sdk.PlatformClient.
func (r *FakeRepo) CloseChangeRequest(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.CRs {
		if r.CRs[i].ID == id {
			r.CRs[i].State = "closed"
			r.Writes = append(r.Writes, "close:"+id)
			return nil
		}
	}
	return fmt.Errorf("no change request %s", id)
}

// Comment implements sdk.PlatformClient.
func (r *FakeRepo) Comment(_ context.Context, id, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Comments[id] = append(r.Comments[id], body)
	r.Writes = append(r.Writes, "comment:"+id)
	return nil
}
