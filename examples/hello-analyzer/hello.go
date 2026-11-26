// Package hello is the example analyzer from docs/plugins.md. It reports
// projects that have no README file next to their manifest.
package hello

import (
	"context"
	"path"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Analyzer reports projects without a README.
type Analyzer struct {
	cfg Config
}

// Config is the plugin's section under plugins.hello-readme in deepward.yaml.
type Config struct {
	// Severity of the finding.
	Severity sdk.Severity `yaml:"severity" json:"severity"`
}

func init() { sdk.Register(New()) }

// New returns the analyzer with default settings.
func New() *Analyzer { return &Analyzer{cfg: Config{Severity: sdk.SeverityLow}} }

// Info implements sdk.Plugin.
func (a *Analyzer) Info() sdk.Info {
	return sdk.Info{ID: "hello-readme", Kind: sdk.KindAnalyzer, Version: "1.0.0",
		Description: "Reports projects without a README file"}
}

// ConfigTarget implements sdk.Configurable.
func (a *Analyzer) ConfigTarget() any { return &a.cfg }

// Scope implements sdk.Analyzer.
func (a *Analyzer) Scope() sdk.AnalyzerScope { return sdk.ScopeProject }

// Requires implements sdk.Analyzer: it needs the list of project files.
func (a *Analyzer) Requires() []string { return []string{sdk.FactSourceTree} }

// Analyze implements sdk.Analyzer.
func (a *Analyzer) Analyze(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
	if req.Project == nil {
		return nil, nil
	}
	tree, err := sdk.GetFact(ctx, req.Facts, sdk.SourceTreeKey)
	if err != nil {
		return nil, err
	}
	for _, f := range tree.Files {
		if path.Dir(f) == path.Clean(req.Project.Dir) && strings.HasPrefix(strings.ToLower(path.Base(f)), "readme") {
			return nil, nil
		}
	}
	manifest := req.Project.Dir
	if len(req.Project.Manifests) > 0 {
		manifest = req.Project.Manifests[0]
	}
	return []sdk.Finding{{
		Analyzer: a.Info().ID, Rule: "missing-readme", Kind: "documentation", Severity: a.cfg.Severity,
		Project: req.Project.ID, Message: "project " + req.Project.Dir + " has no README",
		Locations: []sdk.Location{{File: &sdk.FileLocation{Path: manifest}}},
	}}, nil
}
