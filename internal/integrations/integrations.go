// Package integrations compiles config-defined integrations
// (integrations.<id> in deepward.yaml) into ordinary analyzer and
// fact-producer plugins that run an external tool and parse its output with
// a registered output format.
package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Integration types.
const (
	TypeAnalyzer     = "analyzer"
	TypeFactProducer = "fact-producer"
)

// Config is one integrations.<id> section.
type Config struct {
	Type        string            `yaml:"type"`
	Description string            `yaml:"description"`
	Tool        string            `yaml:"tool"`
	Constraint  string            `yaml:"constraint"`
	Args        []string          `yaml:"args"`
	Env         map[string]string `yaml:"env"`
	// Output is the output format name (e.g. sarif, cyclonedx).
	Output string `yaml:"output"`
	// Fact is the fact produced by a fact-producer integration.
	Fact string `yaml:"fact"`
	// Scope is project (default) or workspace for analyzers.
	Scope string `yaml:"scope"`
	// Ecosystems limits the integration to projects of these ecosystems.
	Ecosystems []string `yaml:"ecosystems"`
	// Stdout parses standard output instead of {{.OutputFile}}.
	Stdout bool `yaml:"stdout"`
	// OutputPath is the file to parse (template, default {{.OutputFile}});
	// use it for tools that write into a directory ({{.OutputDir}}).
	OutputPath string `yaml:"outputPath"`
	// ExitCodes lists exit codes treated as success (default [0]).
	ExitCodes []int        `yaml:"exitCodes"`
	Timeout   sdk.Duration `yaml:"timeout"`
	// Kind overrides the finding kind (default from the output format).
	Kind string `yaml:"kind"`
}

// TemplateData is available to args and env templates.
type TemplateData struct {
	RepoRoot   string
	ProjectDir string
	ProjectRel string
	ProjectID  string
	Ecosystem  string
	OutputFile string
	// OutputDir is an empty temporary directory.
	OutputDir string
}

// Compile turns the raw integrations sections into plugins. Unknown output
// formats, invalid templates and ID clashes are errors.
func Compile(sections map[string]yaml.Node, file string, reg *sdk.Registry) ([]sdk.Plugin, error) {
	formats := map[string]sdk.OutputFormat{}
	for _, p := range reg.ByKind(sdk.KindOutputFormat) {
		if f, ok := p.(sdk.OutputFormat); ok {
			formats[f.Name()] = f
		}
	}
	ids := make([]string, 0, len(sections))
	for id := range sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []sdk.Plugin
	var errs []error
	for _, id := range ids {
		node := sections[id]
		var c Config
		if err := strictyaml.Decode(&node, &c, "integrations."+id, file); err != nil {
			errs = append(errs, err)
			continue
		}
		p, err := build(id, c, formats, reg)
		if err != nil {
			errs = append(errs, fmt.Errorf("integrations.%s: %w", id, err))
			continue
		}
		out = append(out, p)
	}
	return out, errors.Join(errs...)
}

func build(id string, c Config, formats map[string]sdk.OutputFormat, reg *sdk.Registry) (sdk.Plugin, error) {
	if !sdk.ValidID(id) {
		return nil, fmt.Errorf("integration ID %q is not kebab-case", id)
	}
	if _, clash := reg.Get(id); clash {
		return nil, fmt.Errorf("ID %q is already used by a plugin", id)
	}
	if c.Tool == "" {
		return nil, errors.New("tool is required")
	}
	format, ok := formats[c.Output]
	if !ok {
		names := make([]string, 0, len(formats))
		for n := range formats {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown output format %q (available: %s)", c.Output, strings.Join(names, ", "))
	}
	tmpls := make([]*template.Template, 0, len(c.Args))
	for i, a := range c.Args {
		t, err := template.New(fmt.Sprintf("args[%d]", i)).Option("missingkey=error").Parse(a)
		if err != nil {
			return nil, fmt.Errorf("args[%d]: %w", i, err)
		}
		tmpls = append(tmpls, t)
	}
	if len(c.ExitCodes) == 0 {
		c.ExitCodes = []int{0}
	}
	outPath := c.OutputPath
	if outPath == "" {
		outPath = "{{.OutputFile}}"
	}
	outTmpl, err := template.New("outputPath").Option("missingkey=error").Parse(outPath)
	if err != nil {
		return nil, fmt.Errorf("outputPath: %w", err)
	}
	base := &runner{id: id, cfg: c, format: format, args: tmpls, outPath: outTmpl}
	switch c.Type {
	case TypeAnalyzer, "":
		if !produces(format, sdk.OutputFindings) {
			return nil, fmt.Errorf("output format %q does not produce findings", c.Output)
		}
		switch c.Scope {
		case "", string(sdk.ScopeProject), string(sdk.ScopeWorkspace):
		default:
			return nil, fmt.Errorf("scope must be project or workspace, got %q", c.Scope)
		}
		return &Analyzer{runner: base}, nil
	case TypeFactProducer:
		if c.Fact == "" {
			return nil, errors.New("fact is required for a fact-producer integration")
		}
		if !produces(format, c.Fact) {
			return nil, fmt.Errorf("output format %q does not produce the %s fact", c.Output, c.Fact)
		}
		return &FactProducer{runner: base}, nil
	}
	return nil, fmt.Errorf("type must be analyzer or fact-producer, got %q", c.Type)
}

func produces(f sdk.OutputFormat, what string) bool {
	for _, p := range f.Produces() {
		if p == what {
			return true
		}
	}
	return false
}

type runner struct {
	id      string
	cfg     Config
	format  sdk.OutputFormat
	args    []*template.Template
	outPath *template.Template
	host    sdk.Host
}

func (r *runner) info(kind sdk.Kind) sdk.Info {
	desc := r.cfg.Description
	if desc == "" {
		desc = fmt.Sprintf("Runs %s and parses its %s output", r.cfg.Tool, r.cfg.Output)
	}
	return sdk.Info{ID: r.id, Kind: kind, Version: "config", Description: desc, Integration: true}
}

// Init implements sdk.Initializer.
func (r *runner) Init(_ context.Context, h sdk.Host) error { r.host = h; return nil }

// ToolRequirements implements sdk.ToolRequirer.
func (r *runner) ToolRequirements() []sdk.ToolRequirement {
	return []sdk.ToolRequirement{{Name: r.cfg.Tool, Constraint: r.cfg.Constraint, Purpose: "integration " + r.id}}
}

func (r *runner) appliesTo(p sdk.Project) bool {
	if len(r.cfg.Ecosystems) == 0 {
		return true
	}
	for _, e := range r.cfg.Ecosystems {
		if e == p.Ecosystem {
			return true
		}
	}
	return false
}

// run executes the tool and parses its output.
func (r *runner) run(ctx context.Context, ws *sdk.Workspace, project *sdk.Project) (sdk.ParseResult, error) {
	if r.host == nil {
		return sdk.ParseResult{}, errors.New("integration not initialized")
	}
	tool, err := r.host.Tools().Require(ctx, sdk.ToolRequirement{Name: r.cfg.Tool, Constraint: r.cfg.Constraint}, project)
	if err != nil {
		return sdk.ParseResult{}, err
	}
	tmp, err := os.MkdirTemp("", "deepward-"+r.id+"-")
	if err != nil {
		return sdk.ParseResult{}, err
	}
	defer os.RemoveAll(tmp)
	outDir := filepath.Join(tmp, "out")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		return sdk.ParseResult{}, err
	}
	data := TemplateData{OutputFile: filepath.Join(tmp, "output"), OutputDir: outDir}
	dir := ""
	if ws != nil {
		data.RepoRoot = ws.Root
		dir = ws.Root
	}
	if project != nil {
		data.ProjectRel, data.ProjectID, data.Ecosystem = project.Dir, project.ID, project.Ecosystem
		data.ProjectDir = filepath.Join(data.RepoRoot, filepath.FromSlash(project.Dir))
		dir = data.ProjectDir
	} else {
		data.ProjectDir = data.RepoRoot
	}
	args := make([]string, 0, len(r.args))
	for _, t := range r.args {
		var b bytes.Buffer
		if err := t.Execute(&b, data); err != nil {
			return sdk.ParseResult{}, fmt.Errorf("render %s: %w", t.Name(), err)
		}
		args = append(args, b.String())
	}
	res, err := tool.Run(ctx, sdk.RunSpec{Args: args, Dir: dir, Env: r.cfg.Env, Timeout: r.cfg.Timeout.Std()})
	if err != nil {
		var xe *sdk.ExitError
		if !errors.As(err, &xe) || !r.okExit(xe.ExitCode) {
			return sdk.ParseResult{}, err
		}
	}
	var input []byte
	if r.cfg.Stdout {
		input = res.Stdout
	} else {
		var p bytes.Buffer
		if err := r.outPath.Execute(&p, data); err != nil {
			return sdk.ParseResult{}, fmt.Errorf("render outputPath: %w", err)
		}
		input, err = os.ReadFile(p.String())
		if err != nil {
			return sdk.ParseResult{}, fmt.Errorf("%s wrote no output file (use {{.OutputFile}} in args or set stdout: true): %w", r.cfg.Tool, err)
		}
	}
	return r.format.Parse(ctx, bytes.NewReader(input), sdk.ParseRequest{Integration: r.id, Project: project, Workspace: ws, Tool: r.cfg.Tool})
}

func (r *runner) okExit(code int) bool {
	for _, c := range r.cfg.ExitCodes {
		if c == code {
			return true
		}
	}
	return false
}

// Analyzer is a config-defined analyzer.
type Analyzer struct{ *runner }

// Info implements sdk.Plugin.
func (a *Analyzer) Info() sdk.Info { return a.info(sdk.KindAnalyzer) }

// Scope implements sdk.Analyzer.
func (a *Analyzer) Scope() sdk.AnalyzerScope {
	if a.cfg.Scope == string(sdk.ScopeWorkspace) {
		return sdk.ScopeWorkspace
	}
	return sdk.ScopeProject
}

// Requires implements sdk.Analyzer.
func (a *Analyzer) Requires() []string { return nil }

// Analyze implements sdk.Analyzer.
func (a *Analyzer) Analyze(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
	if req.Project != nil && !a.appliesTo(*req.Project) {
		return nil, nil
	}
	res, err := a.run(ctx, req.Workspace, req.Project)
	if err != nil {
		return nil, err
	}
	for i := range res.Findings {
		if a.cfg.Kind != "" {
			res.Findings[i].Kind = sdk.FindingKind(a.cfg.Kind)
		}
		if res.Findings[i].Tool == "" {
			res.Findings[i].Tool = a.cfg.Tool
		}
		if res.Findings[i].Project == "" && req.Project != nil {
			res.Findings[i].Project = req.Project.ID
		}
	}
	return res.Findings, nil
}

// FactProducer is a config-defined fact producer.
type FactProducer struct{ *runner }

// Info implements sdk.Plugin.
func (p *FactProducer) Info() sdk.Info { return p.info(sdk.KindFactProducer) }

// Produces implements sdk.FactProducer.
func (p *FactProducer) Produces() []string { return []string{p.cfg.Fact} }

// AppliesTo implements sdk.FactProducer.
func (p *FactProducer) AppliesTo(pr sdk.Project) bool { return p.appliesTo(pr) }

// Produce implements sdk.FactProducer.
func (p *FactProducer) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	project := req.Project
	res, err := p.run(ctx, req.Workspace, &project)
	if err != nil {
		return nil, err
	}
	if res.Fact == nil {
		return nil, fmt.Errorf("output format %s returned no %s fact", p.cfg.Output, p.cfg.Fact)
	}
	return res.Fact, nil
}
