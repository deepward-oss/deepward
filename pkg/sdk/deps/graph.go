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

// Node is a resolved package. ID is the full purl (with version).
type Node struct {
	ID        string    `json:"id"`
	Ecosystem Ecosystem `json:"ecosystem"`
	// Name is the package coordinates (group:artifact or NuGet ID).
	Name    string `json:"name"`
	Version string `json:"version"`
	Scope   string `json:"scope,omitempty"`
	Direct  bool   `json:"direct"`
	// Attributes carries extra details: scalaBase, scalaSuffix, module,
	// targetFramework, ...
	Attributes map[string]string `json:"attributes,omitempty"`
}

// PURL parses the node ID.
func (n Node) PURL() purl.PURL { return purl.MustParse(n.ID) }

// Edge is a dependency from one node to another (IDs).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is a project's resolved dependency graph. Root is a synthetic node
// for the project itself; direct dependencies are its children.
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
		if n.ID != root {
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
		for _, p := range g.parents[g.Nodes[i].ID] {
			if p == root {
				g.Nodes[i].Direct = true
			}
		}
	}
	return g, nil
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

// Packages returns all nodes except the root.
func (g *Graph) Packages() []Node {
	out := make([]Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID != g.Root {
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
// reachable (id itself if it is direct), sorted.
func (g *Graph) DirectAncestors(id string) []string {
	if g.index == nil {
		g.reindex()
	}
	if _, ok := g.index[id]; !ok || id == g.Root {
		return nil
	}
	// Ancestors by reverse reachability, stopping at the root's children.
	direct := map[string]bool{}
	for _, c := range g.children[g.Root] {
		direct[c] = true
	}
	seen := map[string]bool{id: true}
	stack := []string{id}
	var out []string
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if direct[cur] {
			out = append(out, cur)
		}
		for _, p := range g.parents[cur] {
			if p != g.Root && !seen[p] {
				seen[p] = true
				stack = append(stack, p)
			}
		}
	}
	sort.Strings(out)
	return out
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
