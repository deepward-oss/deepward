// Package sarifimport ingests SARIF 2.1.0 files produced by external tools
// so their results join deepward's reports, quality gate and ignore rules.
package sarifimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sarif"
)

// ID is the plugin ID.
const ID = "sarif-import"

// Config is plugins.sarif-import (also filled by --import-sarif <tool>=<path>).
type Config struct {
	// Files maps a tool label to a SARIF file (relative to the repository root).
	Files map[string]string `yaml:"files" json:"files"`
}

// Analyzer imports SARIF files.
type Analyzer struct{ cfg Config }

func init() { sdk.Register(New()) }

// New returns the analyzer.
func New() *Analyzer { return &Analyzer{} }

// Info implements sdk.Plugin.
func (a *Analyzer) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindAnalyzer, Version: sdk.BuiltinVersion,
		Description: "Imports SARIF 2.1.0 results of external tools (--import-sarif <tool>=<path>)"}
}

// ConfigTarget implements sdk.Configurable.
func (a *Analyzer) ConfigTarget() any { return &a.cfg }

// Scope implements sdk.Analyzer.
func (a *Analyzer) Scope() sdk.AnalyzerScope { return sdk.ScopeWorkspace }

// Requires implements sdk.Analyzer.
func (a *Analyzer) Requires() []string { return nil }

// Analyze implements sdk.Analyzer. An invalid file fails the whole import
// with the validation error.
func (a *Analyzer) Analyze(ctx context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
	tools := make([]string, 0, len(a.cfg.Files))
	for tool := range a.cfg.Files {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	var out []sdk.Finding
	for _, tool := range tools {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := a.cfg.Files[tool]
		if !filepath.IsAbs(p) && req.Workspace != nil {
			p = filepath.Join(req.Workspace.Root, filepath.FromSlash(p))
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", tool, err)
		}
		log, err := sarif.Decode(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("import %s (%s): %w", tool, a.cfg.Files[tool], err)
		}
		findings, err := sarif.ToFindings(log, ID, tool)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", tool, err)
		}
		for i := range findings {
			findings[i].Analyzer, findings[i].Tool = ID, tool
		}
		out = append(out, findings...)
	}
	return out, nil
}
