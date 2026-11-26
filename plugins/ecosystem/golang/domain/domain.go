// Package domain is the pure logic of the Go modules ecosystem: building the
// dependency graph from `go list -m -json all` and `go mod graph`, and the
// GOPROXY / GOPRIVATE rules for version discovery.
package domain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/module"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// Module is one entry of `go list -m -json all`.
type Module struct {
	Path     string  `json:"Path"`
	Version  string  `json:"Version"`
	Main     bool    `json:"Main"`
	Indirect bool    `json:"Indirect"`
	Replace  *Module `json:"Replace"`
	Error    *struct {
		Err string `json:"Err"`
	} `json:"Error"`
}

// ParseList decodes the JSON stream printed by `go list -m -json all`.
func ParseList(r io.Reader) ([]Module, error) {
	dec := json.NewDecoder(r)
	var out []Module
	for {
		var m Module
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		if m.Error != nil && m.Error.Err != "" {
			return nil, fmt.Errorf("go list: %s: %s", m.Path, m.Error.Err)
		}
		out = append(out, m)
	}
}

// BuildGraph combines the selected modules with the edges of `go mod graph`.
// Edges from versions that minimal version selection did not select are
// dropped, and edges point to the selected version of their target. Modules
// replaced by a local directory are not nodes; modules replaced by another
// module appear under the replacement's path and version. scopes maps module
// paths to their scope (see Scopes); nil gives every module scope compile,
// and modules missing from a non-nil map are not built.
func BuildGraph(root, source string, mods []Module, graph []byte, scopes map[string]string) (*deps.Graph, error) {
	type sel struct {
		version string
		id      string
		direct  bool
	}
	selected := map[string]sel{}
	main := map[string]bool{}
	var nodes []deps.Node
	for _, m := range mods {
		if m.Main {
			main[m.Path] = true
			continue
		}
		name, version := m.Path, m.Version
		attrs := map[string]string{}
		if r := m.Replace; r != nil {
			if r.Version == "" {
				selected[m.Path] = sel{version: m.Version}
				continue
			}
			name, version = r.Path, r.Version
			attrs["replaces"] = m.Path + "@" + m.Version
		}
		if version == "" {
			continue
		}
		id := deps.NewPURL(deps.Go, name, version).String()
		selected[m.Path] = sel{version: m.Version, id: id, direct: !m.Indirect}
		if len(attrs) == 0 {
			attrs = nil
		}
		scope := deps.ScopeCompile
		if scopes != nil {
			scope = deps.ScopeNotBuilt
			if s, ok := scopes[m.Path]; ok {
				scope = s
			}
		}
		nodes = append(nodes, deps.Node{ID: id, Ecosystem: deps.Go, Name: name, Version: version, Scope: scope, Attributes: attrs})
	}
	var edges []deps.Edge
	for _, s := range selected {
		if s.direct && s.id != "" {
			edges = append(edges, deps.Edge{From: root, To: s.id})
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(graph))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		fromPath, fromVer, _ := strings.Cut(f[0], "@")
		toPath, _, _ := strings.Cut(f[1], "@")
		if toPath == "go" || toPath == "toolchain" || fromPath == "go" || fromPath == "toolchain" {
			continue
		}
		to, ok := selected[toPath]
		if !ok || to.id == "" {
			continue
		}
		if fromVer == "" && main[fromPath] {
			// Root edges come from the Indirect flags above.
			continue
		}
		from, ok := selected[fromPath]
		if !ok || from.id == "" || from.version != fromVer {
			continue
		}
		edges = append(edges, deps.Edge{From: from.id, To: to.id})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("go mod graph output: %w", err)
	}
	return deps.NewGraph(root, source, nodes, edges)
}

// Proxy is one GOPROXY entry.
type Proxy struct {
	URL string
	// FallbackOnError is true when the entry is followed by "|": the next
	// proxy is tried after any error, not only after 404/410.
	FallbackOnError bool
}

// ParseGOPROXY returns the proxies of a GOPROXY value up to the first
// "direct" or "off" entry.
func ParseGOPROXY(v string) []Proxy {
	if strings.TrimSpace(v) == "" {
		v = "https://proxy.golang.org,direct"
	}
	var out []Proxy
	for v != "" {
		i := strings.IndexAny(v, ",|")
		entry, sep := v, byte(0)
		if i >= 0 {
			entry, sep, v = v[:i], v[i], v[i+1:]
		} else {
			v = ""
		}
		entry = strings.TrimSpace(entry)
		if entry == "direct" || entry == "off" {
			break
		}
		if entry == "" {
			continue
		}
		out = append(out, Proxy{URL: strings.TrimSuffix(entry, "/"), FallbackOnError: sep == '|'})
	}
	return out
}

// Private reports whether a module must not be fetched through a proxy
// (GONOPROXY, defaulting to GOPRIVATE).
func Private(noproxy, private, path string) bool {
	patterns := noproxy
	if patterns == "" {
		patterns = private
	}
	return patterns != "" && module.MatchPrefixPatterns(patterns, path)
}

// ListURL returns the proxy URL listing a module's versions.
func ListURL(proxy, path string) (string, error) {
	esc, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	return proxy + "/" + esc + "/@v/list", nil
}

// CanonicalVersion adds the leading "v" Go versions need.
func CanonicalVersion(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// ModuleList parses the module paths printed by
// `go list -deps -f '{{with .Module}}{{.Path}}{{end}}'`.
func ModuleList(out []byte) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if p := strings.TrimSpace(line); p != "" {
			set[p] = true
		}
	}
	return set
}

// Scopes combines the modules of the build and of build plus tests into
// module scopes: compile for built modules, test for modules only tests
// need. It returns nil when nothing is known to be built, so callers keep
// every module in scope compile instead of hiding them all.
func Scopes(built, withTests map[string]bool) map[string]string {
	if len(built) == 0 {
		return nil
	}
	out := map[string]string{}
	for p := range withTests {
		out[p] = deps.ScopeTest
	}
	for p := range built {
		out[p] = deps.ScopeCompile
	}
	return out
}
