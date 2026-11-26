// Package sarif is the "sarif" output format for config-defined integrations.
package sarif

import (
	"context"
	"io"

	"github.com/deepward-oss/deepward/pkg/sdk"
	sarifmap "github.com/deepward-oss/deepward/pkg/sdk/sarif"
)

// Format parses SARIF 2.1.0 into findings.
type Format struct{}

func init() { sdk.Register(Format{}) }

// Info implements sdk.Plugin.
func (Format) Info() sdk.Info {
	return sdk.Info{ID: "sarif-format", Kind: sdk.KindOutputFormat, Version: sdk.BuiltinVersion, Description: "Parses SARIF 2.1.0 output of external tools into findings"}
}

// Name implements sdk.OutputFormat.
func (Format) Name() string { return "sarif" }

// Produces implements sdk.OutputFormat.
func (Format) Produces() []string { return []string{sdk.OutputFindings} }

// Parse implements sdk.OutputFormat.
func (Format) Parse(_ context.Context, r io.Reader, req sdk.ParseRequest) (sdk.ParseResult, error) {
	log, err := sarifmap.Decode(r)
	if err != nil {
		return sdk.ParseResult{}, err
	}
	findings, err := sarifmap.ToFindings(log, req.Integration, req.Tool)
	if err != nil {
		return sdk.ParseResult{}, err
	}
	for i := range findings {
		if req.Project != nil && findings[i].Project == "" {
			findings[i].Project = req.Project.ID
		}
	}
	return sdk.ParseResult{Findings: findings}, nil
}
