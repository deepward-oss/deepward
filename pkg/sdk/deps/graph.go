package deps

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// FactDependencyGraph is the name of the dependency-graph fact.
const FactDependencyGraph = "dependency-graph"

// GraphKey is the typed key of the dependency-graph fact.
var GraphKey = sdk.NewFactKey[*Graph](FactDependencyGraph)

// Scope values used in the graph.
const (
	ScopeCompile  = "compile"
	ScopeRuntime  = "runtime"
	ScopeProvided = "provided"
	ScopeTest     = "test"
	ScopeSystem   = "system"
	// ScopeDevelopment marks development-only packages (npm devDependencies,
	// Cargo dev-dependencies).
	ScopeDevelopment = "development"
	// ScopeBuild marks build-time packages (Cargo build-dependencies).
	ScopeBuild = "build"
	// ScopeNotBuilt marks packages that are in the resolved graph but not
	// compiled into the project (e.g. Go modules none of whose packages are
	// imported).
	ScopeNotBuilt = "not-built"
)

// Node is a resolved package or a module of a multi-module build. A
// package's ID is its full purl (with version); a module's ID starts with
// "module:" (see ModuleID).
type Node struct {
	ID        string    `json:"id"`
	Ecosystem Ecosystem `json:"ecosystem"`
	// Name is the package coordinates (group:artifact or NuGet ID), or the
	// module's display name (artifactId, subproject ID, project file name).
	Name    string `json:"name"`
	Version string `json:"version"`
	Scope   string `json:"scope,omitempty"`
	Direct  bool   `json:"direct"`
	// Kind is "" for packages and KindModule for modules.
	Kind string `json:"kind,omitempty"`
	// Modules maps the ID of every module that reaches a package to the
	// package's scope in that module. It is set only in graphs with modules.
	Modules map[string]string `json:"modules,omitempty"`
	// Attributes carries extra details: scalaBase, scalaSuffix, module,
	// targetFramework, manifest (of a module), ...
	Attributes map[string]string `json:"attributes,omitempty"`
}

// KindModule marks module nodes: members of a multi-module build (a Maven
// module, an sbt subproject, a .NET project of a solution). The project
// root points at its modules, and modules point at the packages they
// declare and at the modules they depend on.
const KindModule = "module"

// modulePrefix starts the ID of every module node.
const modulePrefix = "module:"

// ModuleID returns the node ID of a module from a key that is unique in the
// project, such as the path of its manifest or its subproject ID.
func ModuleID(key string) string { return modulePrefix + key }

// IsModule reports whether the node is a module.
func (n Node) IsModule() bool { return n.Kind == KindModule }

// PURL parses the node ID.
func (n Node) PURL() purl.PURL { return purl.MustParse(n.ID) }

// Edge is a dependency from one node to another (IDs).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is a project's resolved dependency graph. Root is a synthetic node
// for the project itself. Without modules, direct dependencies are its
// children; with modules, the root's children are the modules and a
// package is direct when a module declares it.
type Graph struct {
	Root  string `json:"root"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Source tells how the graph was produced (e.g. "mvn dependency:tree",
	// "cyclonedx:build/bom.json").
	Source string `json:"source"`

	index    map[string]int
	children map[string][]string
	parents  map[string][]string
}

// NewGraph builds a graph and normalizes it: nodes and edges are sorted and
// deduplicated, Direct flags derived from root edges, and node IDs for
// Scala artifacts get scalaBase/scalaSuffix attributes.
func NewGraph(root, source string, nodes []Node, edges []Edge) (*Graph, error) {
	g := &Graph{Root: root, Source: source}
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		switch {
		case n.ID == root:
		case n.IsModule():
			if !strings.HasPrefix(n.ID, modulePrefix) {
				return nil, fmt.Errorf("module node %q: the ID must start with %q", n.ID, modulePrefix)
			}
		case strings.HasPrefix(n.ID, modulePrefix):
			return nil, fmt.Errorf("node %q: only module nodes may use the %q prefix", n.ID, modulePrefix)
		default:
			p, err := purl.Parse(n.ID)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", n.ID, err)
			}
			if p.Type == purl.TypeMaven {
				if base, suffix := purl.SplitScala(p.Name); suffix != "" {
					if n.Attributes == nil {
						n.Attributes = map[string]string{}
					}
					n.Attributes["scalaBase"] = base
					n.Attributes["scalaSuffix"] = suffix
				}
			}
		}
		g.Nodes = append(g.Nodes, n)
	}
	if !seen[root] {
		g.Nodes = append(g.Nodes, Node{ID: root, Name: root})
	}
	edgeSeen := map[Edge]bool{}
	for _, e := range edges {
		if edgeSeen[e] || e.From == e.To {
			continue
		}
		edgeSeen[e] = true
		g.Edges = append(g.Edges, e)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	g.reindex()
	for _, e := range g.Edges {
		if _, ok := g.index[e.From]; !ok {
			return nil, fmt.Errorf("edge %s -> %s: unknown source node", e.From, e.To)
		}
		if _, ok := g.index[e.To]; !ok {
			return nil, fmt.Errorf("edge %s -> %s: unknown target node", e.From, e.To)
		}
	}
	for i := range g.Nodes {
		if g.Nodes[i].ID == root {
			continue
		}
		g.Nodes[i].Direct = false
		if g.Nodes[i].IsModule() {
			continue
		}
		for _, p := range g.parents[g.Nodes[i].ID] {
			if g.declares(p) {
				g.Nodes[i].Direct = true
			}
		}
	}
	g.fillModules()
	return g, nil
}

// declares reports whether a parent makes its children direct: the root or
// a module.
func (g *Graph) declares(id string) bool {
	if id == g.Root {
		return true
	}
	i, ok := g.index[id]
	return ok && g.Nodes[i].IsModule()
}

// fillModules records, for every package, the modules that reach it
// through packages. The walk does not cross other modules: what a module
// gets through a module it depends on (Maven inlined subtrees, sbt update
// reports, NuGet assets) is in the per-module data the plugin sets, which
// is kept as is; modules the plugin did not mention get the package scope.
func (g *Graph) fillModules() {
	for _, m := range g.Nodes {
		if !m.IsModule() {
			continue
		}
		seen := map[string]bool{m.ID: true}
		queue := []string{m.ID}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, c := range g.children[cur] {
				if seen[c] {
					continue
				}
				seen[c] = true
				n := &g.Nodes[g.index[c]]
				if n.IsModule() {
					continue
				}
				queue = append(queue, c)
				if n.Modules == nil {
					n.Modules = map[string]string{}
				}
				if _, ok := n.Modules[m.ID]; !ok {
					n.Modules[m.ID] = n.Scope
				}
			}
		}
	}
}

func (g *Graph) reindex() {
	g.index = make(map[string]int, len(g.Nodes))
	g.children = map[string][]string{}
	g.parents = map[string][]string{}
	for i, n := range g.Nodes {
		g.index[n.ID] = i
	}
	for _, e := range g.Edges {
		g.children[e.From] = append(g.children[e.From], e.To)
		g.parents[e.To] = append(g.parents[e.To], e.From)
	}
}

// ReportFact embeds the graph in the scan result.
func (g *Graph) ReportFact() any { return g }

// Node returns a node by ID.
func (g *Graph) Node(id string) (Node, bool) {
	if g.index == nil {
		g.reindex()
	}
	i, ok := g.index[id]
	if !ok {
		return Node{}, false
	}
	return g.Nodes[i], true
}

// Packages returns all package nodes (neither the root nor modules).
func (g *Graph) Packages() []Node {
	out := make([]Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID != g.Root && !n.IsModule() {
			out = append(out, n)
		}
	}
	return out
}

// Modules returns the module nodes, sorted by ID.
func (g *Graph) Modules() []Node {
	var out []Node
	for _, n := range g.Nodes {
		if n.IsModule() {
			out = append(out, n)
		}
	}
	return out
}

// Children returns the direct dependencies of a node.
func (g *Graph) Children(id string) []string {
	if g.index == nil {
		g.reindex()
	}
	return g.children[id]
}

// Affected returns what a change of the package id may alter, for
// sdk.Remediation.Affects: the package and every package it currently brings
// in (transitively), as sorted package URLs without version.
func (g *Graph) Affected(id string) []string {
	seen := map[string]bool{}
	keys := map[string]bool{}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		key := cur
		if p, err := purl.Parse(cur); err == nil {
			key = p.WithoutVersion().String()
		}
		keys[key] = true
		stack = append(stack, g.Children(cur)...)
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maxPaths caps Paths when no limit is given.
const maxPaths = 10000

// Paths returns up to limit dependency chains from the root to id, shortest
// first (each chain starts at the root's first child and ends with id).
// Chains are enumerated best-first with the exact distance to id as the
// heuristic, so only partial chains that can still reach id are explored and
// the work grows with the number of chains returned, not with the number of
// all chains in the graph. limit <= 0 returns all chains (at most 10000).
func (g *Graph) Paths(id string, limit int) [][]string {
	if g.index == nil {
		g.reindex()
	}
	if _, ok := g.index[id]; !ok || id == g.Root {
		return nil
	}
	if limit <= 0 || limit > maxPaths {
		limit = maxPaths
	}
	// Distance of every ancestor to id.
	toID := map[string]int{id: 0}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range g.parents[cur] {
			if _, seen := toID[p]; !seen {
				toID[p] = toID[cur] + 1
				queue = append(queue, p)
			}
		}
	}
	if _, ok := toID[g.Root]; !ok {
		return nil
	}
	pq := &pathQueue{}
	heap.Push(pq, &partialPath{nodes: []string{g.Root}, cost: toID[g.Root], key: g.Root})
	var out [][]string
	for pq.Len() > 0 && len(out) < limit {
		cur := heap.Pop(pq).(*partialPath)
		last := cur.nodes[len(cur.nodes)-1]
		if last == id {
			out = append(out, cur.nodes[1:])
			continue
		}
		for _, c := range g.children[last] {
			d, ok := toID[c]
			if !ok || contains(cur.nodes, c) {
				continue
			}
			nodes := append(append(make([]string, 0, len(cur.nodes)+1), cur.nodes...), c)
			heap.Push(pq, &partialPath{nodes: nodes, cost: len(nodes) - 1 + d, key: cur.key + "\x00" + c})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return strings.Join(out[i], "\x00") < strings.Join(out[j], "\x00")
	})
	return out
}

type partialPath struct {
	nodes []string
	cost  int
	key   string
}

type pathQueue []*partialPath

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return q[i].key < q[j].key
}
func (q pathQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(x any)   { *q = append(*q, x.(*partialPath)) }
func (q *pathQueue) Pop() any {
	old := *q
	x := old[len(old)-1]
	*q = old[:len(old)-1]
	return x
}

// DirectAncestors returns the direct dependencies through which id is
// reachable (id itself if it is direct), closest first: ordered by the length
// of the shortest chain from the ancestor down to id, then by ID. The closest
// ancestor is the one whose version decides the transitive version under
// nearest-wins resolution, so it is the one worth upgrading.
func (g *Graph) DirectAncestors(id string) []string {
	if g.index == nil {
		g.reindex()
	}
	if _, ok := g.index[id]; !ok || id == g.Root {
		return nil
	}
	direct := map[string]bool{}
	for _, n := range g.Nodes {
		if n.IsModule() || n.ID == g.Root {
			continue
		}
		for _, p := range g.parents[n.ID] {
			if g.declares(p) {
				direct[n.ID] = true
			}
		}
	}
	// Breadth-first over parents gives every ancestor its distance to id;
	// modules and the root end the walk.
	dist := map[string]int{id: 0}
	queue := []string{id}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if direct[cur] {
			out = append(out, cur)
		}
		for _, p := range g.parents[cur] {
			if _, seen := dist[p]; !seen && !g.declares(p) {
				dist[p] = dist[cur] + 1
				queue = append(queue, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if dist[out[i]] != dist[out[j]] {
			return dist[out[i]] < dist[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Chain returns the shortest dependency chain from one node down to another,
// both included (the lexicographically smallest among equally short chains),
// or nil if to is not reachable from from.
func (g *Graph) Chain(from, to string) []string {
	if g.index == nil {
		g.reindex()
	}
	if _, ok := g.index[to]; !ok {
		return nil
	}
	if from == to {
		return []string{from}
	}
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		kids := append([]string(nil), g.children[cur]...)
		sort.Strings(kids)
		for _, c := range kids {
			if _, seen := prev[c]; seen {
				continue
			}
			prev[c] = cur
			if c != to {
				queue = append(queue, c)
				continue
			}
			chain := []string{to}
			for p := cur; p != ""; p = prev[p] {
				chain = append([]string{p}, chain...)
			}
			return chain
		}
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Validate checks structural invariants.
func (g *Graph) Validate() error {
	var errs []error
	if g.Root == "" {
		errs = append(errs, errors.New("graph has no root"))
	}
	if _, ok := g.Node(g.Root); !ok {
		errs = append(errs, errors.New("root node missing"))
	}
	for _, n := range g.Packages() {
		if n.Version == "" {
			errs = append(errs, fmt.Errorf("node %s has no version", n.ID))
		}
	}
	return errors.Join(errs...)
}

// ModuleRefs lists the modules that reach a package, sorted by name, with
// the package scope in each and whether the module declares it itself.
func (g *Graph) ModuleRefs(id string) []sdk.ModuleRef {
	n, ok := g.Node(id)
	if !ok || len(n.Modules) == 0 {
		return nil
	}
	out := make([]sdk.ModuleRef, 0, len(n.Modules))
	for mid, scope := range n.Modules {
		name := mid
		if m, ok := g.Node(mid); ok && m.Name != "" {
			name = m.Name
		}
		out = append(out, sdk.ModuleRef{ID: mid, Name: name, Scope: scope, Direct: contains(g.children[mid], id)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// DeclaringModule returns the module that declares a package on the
// shortest path to target (the package itself or a package below it), then
// the first by name; ok is false when no module declares it.
func (g *Graph) DeclaringModule(declared, target string) (Node, bool) {
	for _, path := range g.Paths(target, 0) {
		if len(path) < 2 {
			continue
		}
		if m, ok := g.Node(path[0]); ok && m.IsModule() && path[1] == declared {
			return m, true
		}
	}
	var best *Node
	for _, p := range g.parents[declared] {
		if m, ok := g.Node(p); ok && m.IsModule() && (best == nil || m.Name < best.Name) {
			mm := m
			best = &mm
		}
	}
	if best == nil {
		return Node{}, false
	}
	return *best, true
}

// EntryModules returns the modules through which a package enters the
// project from outside: the modules that reach it without passing through
// another module, sorted by ID. It is empty in graphs without modules.
func (g *Graph) EntryModules(id string) []string {
	if g.index == nil {
		g.reindex()
	}
	seen := map[string]bool{id: true}
	queue := []string{id}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range g.parents[cur] {
			if seen[p] || p == g.Root {
				continue
			}
			seen[p] = true
			if n, ok := g.Node(p); ok && n.IsModule() {
				out = append(out, p)
				continue
			}
			queue = append(queue, p)
		}
	}
	sort.Strings(out)
	return out
}
