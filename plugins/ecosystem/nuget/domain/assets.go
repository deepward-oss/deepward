// Package domain turns NuGet's obj/project.assets.json into a dependency graph.
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

// Library is one entry of a target ("Name/Version").
type Library struct {
	Type         string            `json:"type"`
	Dependencies map[string]string `json:"dependencies"`
}

// Assets is the subset of project.assets.json deepward reads.
type Assets struct {
	Version int                           `json:"version"`
	Targets map[string]map[string]Library `json:"targets"`
	Project struct {
		Restore struct {
			ProjectName string `json:"projectName"`
		} `json:"restore"`
		Frameworks map[string]struct {
			Dependencies map[string]struct {
				Target  string `json:"target"`
				Version string `json:"version"`
			} `json:"dependencies"`
		} `json:"frameworks"`
	} `json:"project"`
}

// ParseAssets decodes project.assets.json.
func ParseAssets(r io.Reader) (*Assets, error) {
	var a Assets
	if err := json.NewDecoder(r).Decode(&a); err != nil {
		return nil, fmt.Errorf("invalid project.assets.json: %w", err)
	}
	if a.Version < 3 || a.Targets == nil {
		return nil, errors.New("unsupported project.assets.json (need version 3 with targets)")
	}
	return &a, nil
}

func splitKey(key string) (string, string, bool) {
	i := strings.LastIndex(key, "/")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// framework returns the framework of a target key ("net8.0/linux-x64" -> "net8.0").
func framework(target string) string {
	fw, _, _ := strings.Cut(target, "/")
	return fw
}

// BuildGraph merges several assets files (one per project file of the
// project directory) into one graph. Project-to-project references are part
// of the build: their dependencies become direct dependencies of the root.
func BuildGraph(root, source string, all []*Assets) (*deps.Graph, error) {
	type acc struct {
		node deps.Node
		fws  map[string]bool
	}
	nodes := map[string]*acc{}
	var edges []deps.Edge
	for _, a := range all {
		targets := make([]string, 0, len(a.Targets))
		for t := range a.Targets {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		for _, t := range targets {
			libs := a.Targets[t]
			fw := framework(t)
			// Resolve names case-insensitively within the target.
			byName := map[string]string{}
			kind := map[string]string{}
			for key, lib := range libs {
				name, ver, ok := splitKey(key)
				if !ok {
					continue
				}
				byName[strings.ToLower(name)] = name + "/" + ver
				kind[name+"/"+ver] = lib.Type
			}
			nodeID := func(key string) string {
				name, ver, _ := splitKey(key)
				return purl.New(purl.TypeNuGet, "", name, ver).String()
			}
			isPackage := func(key string) bool { return kind[key] == "package" }
			for key := range libs {
				if !isPackage(key) {
					continue
				}
				name, ver, ok := splitKey(key)
				if !ok {
					continue
				}
				id := nodeID(key)
				n := nodes[id]
				if n == nil {
					n = &acc{node: deps.Node{ID: id, Ecosystem: deps.NuGet, Name: name, Version: ver, Scope: deps.ScopeCompile}, fws: map[string]bool{}}
					nodes[id] = n
				}
				n.fws[fw] = true
			}
			link := func(from string, depName string) {
				key, ok := byName[strings.ToLower(depName)]
				if !ok || !isPackage(key) {
					return
				}
				edges = append(edges, deps.Edge{From: from, To: nodeID(key)})
			}
			for key, lib := range libs {
				from := root
				if isPackage(key) {
					from = nodeID(key)
				} else if kind[key] != "project" {
					continue
				}
				for dep := range lib.Dependencies {
					link(from, dep)
				}
			}
			if f, ok := a.Project.Frameworks[fw]; ok {
				for dep, d := range f.Dependencies {
					if d.Target == "" || strings.EqualFold(d.Target, "Package") {
						link(root, dep)
					}
				}
			}
		}
	}
	list := make([]deps.Node, 0, len(nodes))
	for _, n := range nodes {
		fws := make([]string, 0, len(n.fws))
		for f := range n.fws {
			fws = append(fws, f)
		}
		sort.Strings(fws)
		n.node.Attributes = map[string]string{"targetFrameworks": strings.Join(fws, ",")}
		list = append(list, n.node)
	}
	return deps.NewGraph(root, source, list, edges)
}
