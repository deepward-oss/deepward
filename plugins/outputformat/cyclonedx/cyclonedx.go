// Package cyclonedx is the "cyclonedx" output format: it parses a CycloneDX
// JSON SBOM into the dependency-graph fact.
package cyclonedx

import (
	"context"
	"io"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// Format parses CycloneDX JSON.
type Format struct{}

func init() { sdk.Register(Format{}) }

// Info implements sdk.Plugin.
func (Format) Info() sdk.Info {
	return sdk.Info{ID: "cyclonedx-format", Kind: sdk.KindOutputFormat, Version: sdk.BuiltinVersion, Description: "Parses CycloneDX JSON SBOMs (1.4+) into the dependency-graph fact"}
}

// Name implements sdk.OutputFormat.
func (Format) Name() string { return "cyclonedx" }

// Produces implements sdk.OutputFormat.
func (Format) Produces() []string { return []string{deps.FactDependencyGraph} }

// Parse implements sdk.OutputFormat.
func (Format) Parse(_ context.Context, r io.Reader, req sdk.ParseRequest) (sdk.ParseResult, error) {
	g, err := deps.ParseCycloneDX(r, "cyclonedx:"+req.Integration)
	if err != nil {
		return sdk.ParseResult{}, err
	}
	return sdk.ParseResult{Fact: g}, nil
}
