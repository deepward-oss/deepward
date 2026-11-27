// Package html is the HTML reporter: a single self-contained file (styles
// and scripts inlined, no external resources) rendered from sdk.ScanResult
// with context-aware escaping.
package html

import (
	"context"
	_ "embed"
	"fmt"
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
	"pkgNS":       pkgNS,
	"pkgName":     pkgName,
	"sevShort":    sevShort,
	"shortSHA":    shortSHA,
	"advisoryURL": advisoryURL,
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[fmt.Sprint(kv[i])] = kv[i+1]
		}
		return m
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

// pkgSplit splits a package name into its namespace (with the separator) and
// its own name: "org.x:lib" → "org.x:", "lib"; "@scope/pkg" → "@scope/",
// "pkg"; "github.com/a/b" → "github.com/a/", "b".
func pkgSplit(name string) (string, string) {
	i := strings.LastIndexAny(name, ":/")
	if i <= 0 || i == len(name)-1 {
		return "", name
	}
	return name[:i+1], name[i+1:]
}

func pkgNS(name string) string   { ns, _ := pkgSplit(name); return ns }
func pkgName(name string) string { _, n := pkgSplit(name); return n }

// sevShort abbreviates a severity for the fixed-width severity labels.
func sevShort(s string) string {
	switch s {
	case "critical":
		return "crit"
	case "medium":
		return "med"
	case "unknown":
		return "unkn"
	}
	return s
}

// shortSHA shortens a commit SHA for display.
func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// advisoryURL links an advisory ID: its OSV page when one is referenced,
// else its first reference, else the OSV page by ID.
func advisoryURL(r vulnRow) string {
	for _, ref := range r.References {
		if strings.HasPrefix(ref.URL, "https://osv.dev/vulnerability/") {
			return ref.URL
		}
	}
	if len(r.References) > 0 {
		return r.References[0].URL
	}
	return "https://osv.dev/vulnerability/" + r.ID
}
