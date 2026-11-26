// Package archtest enforces the module boundaries from design D2: the engine
// knows only the SDK, plugins never import each other, processes are started
// only by the toolchain, and domain packages perform no I/O.
package archtest

import (
	"fmt"
	"sort"
	"strings"
)

// Module is the import path of this repository's Go module.
const Module = "github.com/deepward-oss/deepward"

// Package is a node of the import graph: a package and its direct imports.
type Package struct {
	ImportPath string
	Imports    []string
}

// Violation describes one forbidden import edge.
type Violation struct {
	Rule string
	From string
	To   string
}

func (v Violation) String() string { return fmt.Sprintf("%s: %s imports %s", v.Rule, v.From, v.To) }

var domainForbidden = []string{"os", "os/exec", "net", "net/http", "syscall", "io/ioutil"}

// Check applies all rules to the given packages and returns violations sorted
// for stable output.
func Check(pkgs []Package) []Violation {
	var out []Violation
	for _, p := range pkgs {
		rel, ok := strings.CutPrefix(p.ImportPath, Module+"/")
		if !ok {
			continue
		}
		for _, imp := range p.Imports {
			impRel, internal := strings.CutPrefix(imp, Module+"/")
			add := func(rule string) { out = append(out, Violation{Rule: rule, From: p.ImportPath, To: imp}) }

			if hasPrefix(rel, "internal/engine") && internal && hasPrefix(impRel, "plugins") {
				add("engine-is-plugin-agnostic")
			}
			if hasPrefix(rel, "pkg/sdk") && internal && (hasPrefix(impRel, "internal") || hasPrefix(impRel, "plugins")) {
				add("sdk-is-self-contained")
			}
			// plugins/all is the aggregator that registers every built-in plugin.
			if hasPrefix(rel, "plugins") && rel != "plugins/all" && internal && hasPrefix(impRel, "plugins") && pluginRoot(rel) != pluginRoot(impRel) {
				add("plugins-are-isolated")
			}
			if hasPrefix(rel, "plugins") && internal && hasPrefix(impRel, "internal") &&
				!hasPrefix(impRel, "internal/textedit") {
				add("plugins-use-only-sdk")
			}
			if imp == "os/exec" && !hasPrefix(rel, "internal/toolchain") && !hasPrefix(rel, "internal/tools") &&
				!hasPrefix(rel, "internal/archtest") && !hasPrefix(rel, "test") {
				add("processes-only-via-toolchain")
			}
			if isDomain(rel) {
				for _, f := range domainForbidden {
					if imp == f {
						add("domain-has-no-io")
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func hasPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// pluginRoot returns "plugins/<kind>/<name>" for a path inside a plugin.
func pluginRoot(rel string) string {
	parts := strings.SplitN(rel, "/", 4)
	if len(parts) < 3 {
		return rel
	}
	return strings.Join(parts[:3], "/")
}

func isDomain(rel string) bool {
	return strings.HasSuffix(rel, "/domain") || strings.Contains(rel, "/domain/")
}
