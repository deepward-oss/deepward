package domain

import (
	"fmt"
	"sort"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// Tree is the lockfile-independent form of a resolved installation: the
// packages a lockfile records (by a lockfile-specific key) with their
// resolved dependencies, and the local packages (the root and workspace
// members) with their direct dependencies.
type Tree struct {
	Packages  map[string]*Package
	Importers []Importer
	// Format describes the lockfile (e.g. "package-lock.json v3").
	Format string
}

// Package is one installed package.
type Package struct {
	Name    string
	Version string
	// Deps are keys of other packages.
	Deps []string
	// Local marks workspace members and linked folders; they are not nodes.
	Local bool
}

// Importer is a local package with its direct dependencies.
type Importer struct {
	// Dir is the package directory relative to the lockfile ("." for the root).
	Dir  string
	Deps []DirectDep
}

// DirectDep is a resolved direct dependency.
type DirectDep struct {
	Name string
	Key  string
	Dev  bool
}

// Instances returns the versions of every installed copy of a package.
func (t *Tree) Instances(name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range t.Packages {
		if p.Name == name && !p.Local && !seen[p.Version] {
			seen[p.Version] = true
			out = append(out, p.Version)
		}
	}
	sort.Strings(out)
	return out
}

// Graph converts the tree into a dependency graph rooted at root. Only
// packages reachable from the importers become nodes. A package reachable
// through any production dependency has scope runtime, otherwise
// development.
func (t *Tree) Graph(root, source string) (*deps.Graph, error) {
	ids := map[string]string{}
	nodeOf := func(key string) (string, bool) {
		if id, ok := ids[key]; ok {
			return id, id != ""
		}
		p, ok := t.Packages[key]
		if !ok || p.Local || p.Name == "" || p.Version == "" {
			ids[key] = ""
			return "", false
		}
		id := deps.NewPURL(deps.NPM, p.Name, p.Version).String()
		ids[key] = id
		return id, true
	}
	nodes := map[string]*deps.Node{}
	var edges []deps.Edge
	scope := map[string]string{}
	type item struct {
		key string
		dev bool
	}
	var queue []item
	visited := map[item]bool{}
	for _, imp := range t.Importers {
		for _, d := range imp.Deps {
			id, ok := nodeOf(d.Key)
			if !ok {
				continue
			}
			edges = append(edges, deps.Edge{From: root, To: id})
			queue = append(queue, item{d.Key, d.Dev})
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		id, _ := nodeOf(cur.key)
		p := t.Packages[cur.key]
		if _, ok := nodes[id]; !ok {
			nodes[id] = &deps.Node{ID: id, Ecosystem: deps.NPM, Name: p.Name, Version: p.Version}
		}
		if !cur.dev {
			scope[id] = deps.ScopeRuntime
		} else if scope[id] == "" {
			scope[id] = deps.ScopeDevelopment
		}
		for _, dk := range p.Deps {
			did, ok := nodeOf(dk)
			if !ok {
				continue
			}
			edges = append(edges, deps.Edge{From: id, To: did})
			queue = append(queue, item{dk, cur.dev})
		}
	}
	list := make([]deps.Node, 0, len(nodes))
	for id, n := range nodes {
		n.Scope = scope[id]
		list = append(list, *n)
	}
	g, err := deps.NewGraph(root, source, list, edges)
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}
	return g, nil
}
