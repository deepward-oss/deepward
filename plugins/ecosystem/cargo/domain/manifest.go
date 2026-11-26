package domain

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Dependency is one entry of a Cargo.toml dependency table.
type Dependency struct {
	// Key is the table key (the name used in code).
	Key string
	// Package is the crate name (Key unless renamed with `package = ...`).
	Package string
	// Req is the version requirement ("" for path or git only entries).
	Req       string
	Kind      Kind
	Path      bool
	Git       bool
	Workspace bool
}

// Manifest is a parsed Cargo.toml.
type Manifest struct {
	Path              string
	Name              string
	IsWorkspace       bool
	Members           []string
	Exclude           []string
	Deps              []Dependency
	WorkspaceDeps     map[string]Dependency
	HasPackageSection bool
}

// ParseManifest parses Cargo.toml.
func ParseManifest(p string, data []byte) (*Manifest, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	m := &Manifest{Path: p, WorkspaceDeps: map[string]Dependency{}}
	if pkg, ok := raw["package"].(map[string]any); ok {
		m.HasPackageSection = true
		m.Name, _ = pkg["name"].(string)
	}
	if ws, ok := raw["workspace"].(map[string]any); ok {
		m.IsWorkspace = true
		m.Members = strings_(ws["members"])
		m.Exclude = strings_(ws["exclude"])
		if wd, ok := ws["dependencies"].(map[string]any); ok {
			for _, d := range depTable(wd, KindNormal) {
				m.WorkspaceDeps[d.Key] = d
			}
		}
	}
	add := func(tables map[string]any) {
		for _, s := range []struct {
			key  string
			kind Kind
		}{{"dependencies", KindNormal}, {"build-dependencies", KindBuild}, {"build_dependencies", KindBuild}, {"dev-dependencies", KindDev}, {"dev_dependencies", KindDev}} {
			if t, ok := tables[s.key].(map[string]any); ok {
				m.Deps = append(m.Deps, depTable(t, s.kind)...)
			}
		}
	}
	add(raw)
	if targets, ok := raw["target"].(map[string]any); ok {
		for _, name := range sortedKeys(targets) {
			if t, ok := targets[name].(map[string]any); ok {
				add(t)
			}
		}
	}
	// Workspace-inherited entries take the requirement from the root.
	return m, nil
}

func strings_(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func depTable(t map[string]any, kind Kind) []Dependency {
	var out []Dependency
	for _, key := range sortedKeys(t) {
		d := Dependency{Key: key, Package: key, Kind: kind}
		switch v := t[key].(type) {
		case string:
			d.Req = v
		case map[string]any:
			d.Req, _ = v["version"].(string)
			if pkg, ok := v["package"].(string); ok && pkg != "" {
				d.Package = pkg
			}
			_, d.Path = v["path"]
			_, d.Git = v["git"]
			d.Workspace, _ = v["workspace"].(bool)
		}
		out = append(out, d)
	}
	return out
}

// Kinds returns the kind of each dependency by crate name (normal wins over
// build, build over dev).
func (m *Manifest) Kinds() map[string]Kind {
	out := map[string]Kind{}
	for _, d := range m.Deps {
		if d.Kind > out[d.Package] {
			out[d.Package] = d.Kind
		}
	}
	return out
}

// Declarations returns the dependencies of a crate.
func (m *Manifest) Declarations(crate string) []Dependency {
	var out []Dependency
	for _, d := range m.Deps {
		if d.Package == crate {
			out = append(out, d)
		}
	}
	return out
}

// Excluded reports whether a directory relative to the workspace root is in
// workspace.exclude.
func (m *Manifest) Excluded(rel string) bool {
	for _, e := range m.Exclude {
		e = strings.Trim(path.Clean(e), "/")
		if rel == e || strings.HasPrefix(rel, e+"/") {
			return true
		}
	}
	return false
}
