// Package domain is the pure logic of the Cargo ecosystem: Cargo.lock and
// Cargo.toml parsing, graph building with dependency kinds, version
// requirement matching and byte-exact requirement edits.
package domain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// LockPackage is one [[package]] of Cargo.lock.
type LockPackage struct {
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Source       string   `toml:"source"`
	Dependencies []string `toml:"dependencies"`
}

// Lock is a parsed Cargo.lock.
type Lock struct {
	Version  int           `toml:"version"`
	Packages []LockPackage `toml:"package"`
}

// ParseLock parses Cargo.lock.
func ParseLock(data []byte) (*Lock, error) {
	var l Lock
	if _, err := toml.Decode(string(data), &l); err != nil {
		return nil, fmt.Errorf("parse Cargo.lock: %w", err)
	}
	return &l, nil
}

// Registry reports whether a package comes from a registry (not a path or
// git dependency).
func (p LockPackage) Registry() bool {
	return strings.HasPrefix(p.Source, "registry+") || strings.HasPrefix(p.Source, "sparse+")
}

// Instances returns the locked versions of a crate.
func (l *Lock) Instances(name string) []string {
	var out []string
	for _, p := range l.Packages {
		if p.Name == name && p.Registry() {
			out = append(out, p.Version)
		}
	}
	sort.Strings(out)
	return out
}

// resolve finds the package a dependency entry ("name", "name version" or
// "name version (source)") refers to.
func (l *Lock) resolve(entry string) int {
	f := strings.Fields(entry)
	if len(f) == 0 {
		return -1
	}
	match := -1
	for i, p := range l.Packages {
		if p.Name != f[0] || (len(f) > 1 && p.Version != f[1]) {
			continue
		}
		if len(f) > 2 && "("+p.Source+")" != strings.Join(f[2:], " ") {
			continue
		}
		match = i
	}
	return match
}

// Kind is a Cargo dependency kind.
type Kind int

// Dependency kinds, ordered by precedence (normal wins).
const (
	KindDev Kind = iota + 1
	KindBuild
	KindNormal
)

func (k Kind) scope() string {
	switch k {
	case KindNormal:
		return deps.ScopeCompile
	case KindBuild:
		return deps.ScopeBuild
	}
	return deps.ScopeDevelopment
}

// Graph builds the dependency graph. Path and git packages are part of the
// project, not nodes; their dependencies of registry packages are direct.
// kinds gives, per local package name, the kind of each of its
// dependencies (by package name); unknown ones count as normal.
func (l *Lock) Graph(root, source string, kinds map[string]map[string]Kind) (*deps.Graph, error) {
	ids := make([]string, len(l.Packages))
	for i, p := range l.Packages {
		if p.Registry() {
			ids[i] = deps.NewPURL(deps.CratesIO, p.Name, p.Version).String()
		}
	}
	type item struct {
		idx  int
		kind Kind
	}
	var queue []item
	var edges []deps.Edge
	for _, p := range l.Packages {
		if p.Registry() {
			continue
		}
		for _, d := range p.Dependencies {
			j := l.resolve(d)
			if j < 0 || ids[j] == "" {
				continue
			}
			kind := KindNormal
			if k, ok := kinds[p.Name][l.Packages[j].Name]; ok {
				kind = k
			}
			edges = append(edges, deps.Edge{From: root, To: ids[j]})
			queue = append(queue, item{j, kind})
		}
	}
	best := map[int]Kind{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if best[cur.idx] >= cur.kind {
			continue
		}
		best[cur.idx] = cur.kind
		for _, d := range l.Packages[cur.idx].Dependencies {
			j := l.resolve(d)
			if j < 0 || ids[j] == "" {
				continue
			}
			edges = append(edges, deps.Edge{From: ids[cur.idx], To: ids[j]})
			queue = append(queue, item{j, cur.kind})
		}
	}
	var nodes []deps.Node
	for idx, k := range best {
		p := l.Packages[idx]
		nodes = append(nodes, deps.Node{ID: ids[idx], Ecosystem: deps.CratesIO, Name: p.Name, Version: p.Version, Scope: k.scope()})
	}
	return deps.NewGraph(root, source, nodes, edges)
}
