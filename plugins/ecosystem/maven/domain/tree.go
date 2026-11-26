package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

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

// BuildGraph merges module trees into one project graph. Reactor modules are
// the project itself, so they are not graph nodes: their direct dependencies
// become direct dependencies of the project root, annotated with the modules
// that declare them.
func BuildGraph(root, source string, trees []TreeNode) (*deps.Graph, error) {
	modules := map[string]string{}
	for _, t := range trees {
		modules[t.coordinates()] = t.ArtifactID
	}
	type acc struct {
		node    deps.Node
		modules map[string]bool
	}
	nodes := map[string]*acc{}
	var edges []deps.Edge
	var add func(parent string, n TreeNode, module string, direct bool)
	add = func(parent string, n TreeNode, module string, direct bool) {
		if _, isModule := modules[n.coordinates()]; isModule {
			return
		}
		p := purl.New(purl.TypeMaven, n.GroupID, n.ArtifactID, n.Version)
		id := p.String()
		a, ok := nodes[id]
		scope := n.Scope
		if scope == "" {
			scope = deps.ScopeCompile
		}
		if !ok {
			a = &acc{node: deps.Node{ID: id, Ecosystem: deps.Maven, Name: n.coordinates(), Version: n.Version, Scope: scope}, modules: map[string]bool{}}
			nodes[id] = a
		} else if scopeRank[scope] > scopeRank[a.node.Scope] {
			a.node.Scope = scope
		}
		if n.Optional == "true" {
			if a.node.Attributes == nil {
				a.node.Attributes = map[string]string{}
			}
			a.node.Attributes["optional"] = "true"
		}
		if direct {
			a.modules[module] = true
		}
		edges = append(edges, deps.Edge{From: parent, To: id})
		for _, c := range n.Children {
			add(id, c, module, false)
		}
	}
	for _, t := range trees {
		for _, c := range t.Children {
			add(root, c, t.ArtifactID, true)
		}
	}
	list := make([]deps.Node, 0, len(nodes))
	for _, a := range nodes {
		if len(a.modules) > 0 {
			ms := make([]string, 0, len(a.modules))
			for m := range a.modules {
				ms = append(ms, m)
			}
			sort.Strings(ms)
			if a.node.Attributes == nil {
				a.node.Attributes = map[string]string{}
			}
			a.node.Attributes["modules"] = strings.Join(ms, ",")
		}
		list = append(list, a.node)
	}
	return deps.NewGraph(root, source, list, edges)
}
