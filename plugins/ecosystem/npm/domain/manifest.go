// Package domain is the pure logic of the npm ecosystem: package.json and
// lockfile parsing (npm, Yarn, pnpm), graph building, range rewriting and
// byte-exact package.json edits.
package domain

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Manager is a Node.js package manager.
type Manager string

// Package managers.
const (
	NPM  Manager = "npm"
	Yarn Manager = "yarn"
	PNPM Manager = "pnpm"
)

// Lockfiles maps lockfile names to their package managers, in priority order.
var Lockfiles = []struct {
	Name    string
	Manager Manager
}{
	{"package-lock.json", NPM},
	{"npm-shrinkwrap.json", NPM},
	{"pnpm-lock.yaml", PNPM},
	{"yarn.lock", Yarn},
}

// DepKind is a package.json dependency section.
type DepKind string

// Dependency sections.
const (
	KindProd     DepKind = "dependencies"
	KindDev      DepKind = "devDependencies"
	KindOptional DepKind = "optionalDependencies"
	KindPeer     DepKind = "peerDependencies"
)

// EditableKinds are the sections deepward rewrites.
var EditableKinds = []DepKind{KindProd, KindDev, KindOptional}

// Manifest is a parsed package.json.
type Manifest struct {
	// Path is the slash-separated path of the file.
	Path           string
	Name           string
	Version        string
	Deps           map[DepKind]map[string]string
	Workspaces     []string
	PackageManager string
}

// ParseManifest parses package.json.
func ParseManifest(p string, data []byte) (*Manifest, error) {
	var raw struct {
		Name                 string            `json:"name"`
		Version              string            `json:"version"`
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		Workspaces           json.RawMessage   `json:"workspaces"`
		PackageManager       string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	m := &Manifest{Path: p, Name: raw.Name, Version: raw.Version, PackageManager: raw.PackageManager, Deps: map[DepKind]map[string]string{
		KindProd: raw.Dependencies, KindDev: raw.DevDependencies, KindOptional: raw.OptionalDependencies, KindPeer: raw.PeerDependencies,
	}}
	if len(raw.Workspaces) > 0 {
		var list []string
		if err := json.Unmarshal(raw.Workspaces, &list); err != nil {
			var obj struct {
				Packages []string `json:"packages"`
			}
			if err := json.Unmarshal(raw.Workspaces, &obj); err != nil {
				return nil, fmt.Errorf("%s: workspaces must be a list or {packages: [...]}", p)
			}
			list = obj.Packages
		}
		m.Workspaces = list
	}
	return m, nil
}

// Dir returns the directory of the manifest.
func (m *Manifest) Dir() string { return path.Dir(m.Path) }

// Declared returns the sections declaring name (except peerDependencies).
func (m *Manifest) Declared(name string) []DepKind {
	var out []DepKind
	for _, k := range EditableKinds {
		if _, ok := m.Deps[k][name]; ok {
			out = append(out, k)
		}
	}
	return out
}

// DirectDeps returns name → dev flag for the manifest's dependencies. A
// package declared both as a production and a development dependency counts
// as production.
func (m *Manifest) DirectDeps() map[string]bool {
	out := map[string]bool{}
	for _, k := range []DepKind{KindDev, KindProd, KindOptional} {
		for name := range m.Deps[k] {
			out[name] = k == KindDev
		}
	}
	return out
}

// ManagerFromField parses the "packageManager" field ("pnpm@9.1.0+sha...").
func ManagerFromField(field string) (Manager, bool) {
	name, _, _ := strings.Cut(field, "@")
	switch Manager(name) {
	case NPM, Yarn, PNPM:
		return Manager(name), true
	}
	return "", false
}

// MatchWorkspace reports whether a directory (relative to the workspace
// root, slash-separated) matches the workspace patterns. Patterns support
// "*" within a segment, "**" across segments and "!" negation.
func MatchWorkspace(patterns []string, dir string) bool {
	matched := false
	for _, p := range patterns {
		neg := strings.HasPrefix(p, "!")
		p = strings.TrimPrefix(strings.TrimPrefix(p, "!"), "./")
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		if globMatch(strings.Split(p, "/"), strings.Split(dir, "/")) {
			matched = !neg
		}
	}
	return matched
}

func globMatch(pattern, parts []string) bool {
	if len(pattern) == 0 {
		return len(parts) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if globMatch(pattern[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	if ok, _ := path.Match(pattern[0], parts[0]); !ok {
		return false
	}
	return globMatch(pattern[1:], parts[1:])
}

// SortedKeys returns the keys of a map in order.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
