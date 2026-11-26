package sdk

import (
	"context"
	"io"
)

// Ecosystem detects projects of one build system. It may additionally
// implement FactProducer (to build facts for its own projects),
// RemediationRealizer (to edit its manifests), ToolDefiner and AddonProvider.
type Ecosystem interface {
	Plugin
	Detect(ctx context.Context, ws *Workspace) ([]Project, error)
}

// FactProducer computes facts for projects. A standalone FactProducer plugin
// takes precedence over the owning ecosystem when AppliesTo returns true.
type FactProducer interface {
	Plugin
	// Produces lists the fact names this producer can compute.
	Produces() []string
	// AppliesTo reports whether the producer handles the project.
	AppliesTo(p Project) bool
	Produce(ctx context.Context, req FactRequest) (any, error)
}

// FactRequest asks for one fact of one project.
type FactRequest struct {
	Name      string
	Project   Project
	Workspace *Workspace
	// Facts gives access to other facts of the same project.
	Facts Facts
}

// AnalyzerScope tells the engine how to invoke an analyzer.
type AnalyzerScope string

// Analyzer scopes.
const (
	ScopeProject   AnalyzerScope = "project"
	ScopeWorkspace AnalyzerScope = "workspace"
)

// Analyzer produces findings.
type Analyzer interface {
	Plugin
	Scope() AnalyzerScope
	// Requires lists the facts the analyzer needs; if one is unavailable for a
	// project, the analyzer is skipped for it.
	Requires() []string
	Analyze(ctx context.Context, req AnalyzeRequest) ([]Finding, error)
}

// AnalyzeRequest is one analyzer invocation. Project and Facts are nil for
// workspace-scoped analyzers.
type AnalyzeRequest struct {
	Project   *Project
	Workspace *Workspace
	Facts     Facts
	// Projects lists every detected project (useful for workspace analyzers).
	Projects []Project
}

// Reporter renders a scan result.
type Reporter interface {
	Plugin
	// Format is the name used with --format (e.g. "html").
	Format() string
	// FileName is the default output file name.
	FileName() string
	Render(ctx context.Context, result *ScanResult, w io.Writer) error
}

// OutputFormat parses the output of an external tool for config-defined
// integrations.
type OutputFormat interface {
	Plugin
	// Name is the value used in integrations.<id>.output.
	Name() string
	// Produces lists what Parse returns: OutputFindings and/or fact names.
	Produces() []string
	Parse(ctx context.Context, r io.Reader, req ParseRequest) (ParseResult, error)
}

// OutputFindings is the Produces value of formats that yield findings.
const OutputFindings = "findings"

// ParseRequest gives the parser context about the integration run.
type ParseRequest struct {
	Integration string
	Project     *Project
	Workspace   *Workspace
	// Tool is a label for the producing tool (e.g. "semgrep").
	Tool string
}

// ParseResult holds parsed findings or a fact value.
type ParseResult struct {
	Findings []Finding
	Fact     any
}

// RemediationRealizer turns package changes into file edits for projects of
// its ecosystem.
type RemediationRealizer interface {
	Realize(ctx context.Context, req RealizeRequest) (RealizeResult, error)
}

// RealizeRequest asks an ecosystem to apply package changes.
type RealizeRequest struct {
	Project   Project
	Workspace *Workspace
	Changes   []PackageChange
}

// RealizeResult holds the edits for the realizable changes and the reasons
// for the others.
type RealizeResult struct {
	Edits  []TextEdit
	Manual []ManualChange
}

// ManualChange is a change the ecosystem could not apply automatically.
type ManualChange struct {
	Change PackageChange `json:"change"`
	Reason string        `json:"reason"`
	// Suggestion is ready-to-paste text for a human, if available.
	Suggestion string `json:"suggestion,omitempty"`
}

// AddonKind tells how an ecosystem uses an add-on.
type AddonKind string

// Add-on kinds.
const (
	AddonMavenGoal     AddonKind = "maven-goal"
	AddonSbtPluginFile AddonKind = "sbt-plugin-file"
	AddonDotnetTool    AddonKind = "dotnet-tool"
)

// AddonSpec declares an add-on and its defaults.
type AddonSpec struct {
	Name    string    `json:"name"`
	Kind    AddonKind `json:"kind"`
	Default Addon     `json:"default"`
}

// Addon is a resolved add-on: a build-tool plugin or tool the ecosystem
// injects into its invocations without editing project files.
type Addon struct {
	Name string    `yaml:"-" json:"name"`
	Kind AddonKind `yaml:"kind" json:"kind"`
	// Coordinates is e.g. "org.apache.maven.plugins:maven-dependency-plugin"
	// for a Maven goal or the package ID of a dotnet tool.
	Coordinates string `yaml:"coordinates" json:"coordinates,omitempty"`
	Version     string `yaml:"version" json:"version,omitempty"`
	// Goal is the Maven goal (e.g. "tree").
	Goal string `yaml:"goal" json:"goal,omitempty"`
	// File is a path (relative to the repository root) of an sbt file.
	File string `yaml:"file" json:"file,omitempty"`
	// Builtin marks add-ons whose content ships with the plugin.
	Builtin bool `yaml:"-" json:"builtin,omitempty"`
}

// AddonProvider is implemented by ecosystems that declare add-ons.
type AddonProvider interface {
	Addons() []AddonSpec
}

// ToolDefiner is implemented by plugins that contribute tool definitions
// (executable names, wrappers, version probes) to the toolchain catalog.
type ToolDefiner interface {
	ToolDefinitions() []ToolDefinition
}

// ToolRequirer is implemented by plugins that declare the tools they need, so
// `deepward doctor` can check them without running analysis.
type ToolRequirer interface {
	ToolRequirements() []ToolRequirement
}

// DataUpdater is implemented by plugins that keep local data (e.g. an
// offline vulnerability database); `deepward db update` calls it.
type DataUpdater interface {
	UpdateData(ctx context.Context) (summary string, err error)
}
