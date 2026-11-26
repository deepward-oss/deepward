// Package html is the HTML reporter: a single self-contained file (styles
// and scripts inlined, no external resources) rendered from sdk.ScanResult
// with context-aware escaping.
package html

import (
	"context"
	_ "embed"
	"html/template"
	"io"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

var (
	//go:embed assets/report.tmpl
	pageTemplate string
	//go:embed assets/report.css
	css string
	//go:embed assets/report.js
	js string
)

var page = template.Must(template.New("report").Funcs(template.FuncMap{
	"css":   func() template.CSS { return template.CSS(css) },
	"js":    func() template.JS { return template.JS(js) },
	"join":  strings.Join,
	"lower": strings.ToLower,
	"kindTitle": func(k string) string {
		k = strings.ReplaceAll(k, "-", " ")
		if k == "" {
			return "Other findings"
		}
		return strings.ToUpper(k[:1]) + k[1:] + " findings"
	},
	"nonzero": func(cs []count) bool {
		for _, c := range cs {
			if c.Count > 0 {
				return true
			}
		}
		return false
	},
}).Parse(pageTemplate))

// Reporter writes the HTML report.
type Reporter struct{}

func init() { sdk.Register(Reporter{}) }

// Info implements sdk.Plugin.
func (Reporter) Info() sdk.Info {
	return sdk.Info{ID: "html", Kind: sdk.KindReporter, Version: sdk.BuiltinVersion,
		Description: "Self-contained HTML report with filters, sorting and search; readable without JavaScript"}
}

// Format implements sdk.Reporter.
func (Reporter) Format() string { return "html" }

// FileName implements sdk.Reporter.
func (Reporter) FileName() string { return "deepward-report.html" }

// Render implements sdk.Reporter.
func (Reporter) Render(ctx context.Context, res *sdk.ScanResult, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return page.Execute(w, buildView(res))
}
