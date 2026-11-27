package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// TreeNode is one node of `mvn dependency:tree -DoutputType=json`.
type TreeNode struct {
	GroupID    string     `json:"groupId"`
	ArtifactID string     `json:"artifactId"`
	Version    string     `json:"version"`
	Type       string     `json:"type"`
	Scope      string     `json:"scope"`
	Classifier string     `json:"classifier"`
	Optional   string     `json:"optional"`
	Children   []TreeNode `json:"children"`
}

func (n TreeNode) coordinates() string { return n.GroupID + ":" + n.ArtifactID }

// ParseTrees reads the concatenated JSON objects written by the dependency
// plugin with -DappendOutput=true (one per reactor module).
func ParseTrees(r io.Reader) ([]TreeNode, error) {
	dec := json.NewDecoder(r)
	var out []TreeNode
	for {
		var n TreeNode
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid dependency tree JSON: %w", err)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("dependency tree output is empty")
	}
	return out, nil
}

var scopeRank = map[string]int{deps.ScopeCompile: 5, deps.ScopeRuntime: 4, deps.ScopeProvided: 3, deps.ScopeSystem: 2, deps.ScopeTest: 1}

// BuildGraph merges module trees into one project graph. With more than one
// reactor module, every module is a module node (named by its artifactId,
// identified by its POM path from manifests, keyed by "groupId:artifactId")
// that points at its dependencies and at the modules it depends on, and
// every package records its scope in each module. A single-module project
// keeps a flat graph whose root points at the dependencies.
func BuildGraph(root, source string, trees []TreeNode, manifests map[string]string) (*deps.Graph, error) {
	multi := len(trees) > 1
	moduleIDs := map[string]string{}
	var list []deps.Node
	for _, t := range trees {
		key := manifests[t.coordinates()]
		if key == "" {
			key = t.coordinates()
		}
		id := root
		if multi {
			id = deps.ModuleID(key)
			n := deps.Node{ID: id, Ecosystem: deps.Maven, Name: t.ArtifactID, Version: t.Version, Kind: deps.KindModule}
			if path := manifests[t.coordinates()]; path != "" {
				n.Attributes = map[string]string{"manifest": path}
			}
			list = append(list, n)
		}
		moduleIDs[t.coordinates()] = id
	}
	type acc struct {
		node   deps.Node
		scopes map[string]string
	}
	nodes := map[string]*acc{}
	var edges []deps.Edge
	widest := func(a, b string) string {
		if scopeRank[b] > scopeRank[a] {
			return b
		}
		return a
	}
	// add walks n below parent in the tree of module. Inside the inlined
	// subtree of another module (link false) it only records scopes: that
	// module's own tree provides the edges.
	var add func(parent string, n TreeNode, module string, link bool)
	add = func(parent string, n TreeNode, module string, link bool) {
		if id, isModule := moduleIDs[n.coordinates()]; isModule {
			if !multi {
				return
			}
			if link {
				edges = append(edges, deps.Edge{From: parent, To: id})
			}
			for _, c := range n.Children {
				add(id, c, module, false)
			}
			return
		}
		p := purl.New(purl.TypeMaven, n.GroupID, n.ArtifactID, n.Version)
		id := p.String()
		scope := n.Scope
		if scope == "" {
			scope = deps.ScopeCompile
		}
		a, ok := nodes[id]
		if !ok {
			a = &acc{node: deps.Node{ID: id, Ecosystem: deps.Maven, Name: n.coordinates(), Version: n.Version, Scope: scope}, scopes: map[string]string{}}
			nodes[id] = a
		} else {
			a.node.Scope = widest(a.node.Scope, scope)
		}
		if multi {
			a.scopes[module] = widest(a.scopes[module], scope)
		}
		if n.Optional == "true" {
			if a.node.Attributes == nil {
				a.node.Attributes = map[string]string{}
			}
			a.node.Attributes["optional"] = "true"
		}
		if link {
			edges = append(edges, deps.Edge{From: parent, To: id})
		}
		for _, c := range n.Children {
			add(id, c, module, link)
		}
	}
	for _, t := range trees {
		module := moduleIDs[t.coordinates()]
		if multi {
			edges = append(edges, deps.Edge{From: root, To: module})
		}
		for _, c := range t.Children {
			add(module, c, module, true)
		}
	}
	for _, a := range nodes {
		if len(a.scopes) > 0 {
			a.node.Modules = a.scopes
		}
		list = append(list, a.node)
	}
	return deps.NewGraph(root, source, list, edges)
}
