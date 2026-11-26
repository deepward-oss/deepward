// Package json is the JSON reporter: the direct encoding of sdk.ScanResult.
package json

import (
	"context"
	"encoding/json"
	"io"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Reporter writes the scan result as indented JSON.
type Reporter struct{}

func init() { sdk.Register(Reporter{}) }

// Info implements sdk.Plugin.
func (Reporter) Info() sdk.Info {
	return sdk.Info{ID: "json", Kind: sdk.KindReporter, Version: sdk.BuiltinVersion,
		Description: "Machine-readable JSON result (versioned schema, input of `deepward report --from`)"}
}

// Format implements sdk.Reporter.
func (Reporter) Format() string { return "json" }

// FileName implements sdk.Reporter.
func (Reporter) FileName() string { return "deepward-report.json" }

// Render implements sdk.Reporter.
func (Reporter) Render(ctx context.Context, res *sdk.ScanResult, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}
