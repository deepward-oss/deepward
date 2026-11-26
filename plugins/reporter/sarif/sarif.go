// Package sarif is the SARIF 2.1.0 reporter for GitHub code scanning and
// GitLab.
package sarif

import (
	"context"
	"encoding/json"
	"io"

	"github.com/deepward-oss/deepward/pkg/sdk"
	sarifmap "github.com/deepward-oss/deepward/pkg/sdk/sarif"
)

// Reporter writes SARIF.
type Reporter struct{}

func init() { sdk.Register(Reporter{}) }

// Info implements sdk.Plugin.
func (Reporter) Info() sdk.Info {
	return sdk.Info{ID: "sarif", Kind: sdk.KindReporter, Version: sdk.BuiltinVersion,
		Description: "SARIF 2.1.0 for GitHub code scanning and GitLab, located at the declaring manifest"}
}

// Format implements sdk.Reporter.
func (Reporter) Format() string { return "sarif" }

// FileName implements sdk.Reporter.
func (Reporter) FileName() string { return "deepward-report.sarif" }

// Render implements sdk.Reporter. Suppressed findings are included with a
// SARIF suppression so consumers can show them as dismissed.
func (Reporter) Render(ctx context.Context, res *sdk.ScanResult, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	driver := sarifmap.Driver{Name: "deepward", Version: res.Tool.Version, InformationURI: "https://github.com/deepward-oss/deepward"}
	props := map[string]any{}
	if len(res.DataSources) > 0 {
		var credits []map[string]string
		for _, d := range res.DataSources {
			credits = append(credits, map[string]string{"name": d.Name, "license": d.License, "attribution": d.Attribution})
		}
		props["dataSources"] = credits
	}
	if res.Run.Commit != "" {
		props["commit"] = res.Run.Commit
	}
	if len(props) == 0 {
		props = nil
	}
	log := sarifmap.FromFindings(driver, res.Findings, props)
	for i, f := range res.Findings {
		if f.Suppression != nil {
			log.Runs[0].Results[i].Suppressions = []sarifmap.Suppression{{Kind: "external", Justification: f.Suppression.Reason}}
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(log)
}
