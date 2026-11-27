// Package domain turns NuGet's obj/project.assets.json into a dependency graph.
package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
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
	// Libraries describe the libraries of the targets; for project
	// references, Path is the referenced project file relative to the
	// owner's directory.
	Libraries map[string]struct {
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"libraries"`
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

// OwnedAssets is the assets file of one project file (its workspace path).
type OwnedAssets struct {
	Path   string
	Assets *Assets
}

// BuildGraph merges the assets files of the project files of one project.
// With more than one project file, each is a module node (identified by
// its path, named by its file name without extension) that points at the
// packages it references and at the member project files it references;
// every package records each module whose assets contain it. A project
// reference outside the project is not a module: its packages hang off the
// referencing project. With one project file the graph is flat.
func BuildGraph(root, source string, owned []OwnedAssets) (*deps.Graph, error) {
	multi := len(owned) > 1
	moduleOf := map[string]string{}
	var list []deps.Node
	var edges []deps.Edge
	if multi {
		for _, o := range owned {
			id := deps.ModuleID(o.Path)
			moduleOf[o.Path] = id
			name := strings.TrimSuffix(path.Base(o.Path), path.Ext(o.Path))
			list = append(list, deps.Node{ID: id, Ecosystem: deps.NuGet, Name: name, Kind: deps.KindModule,
				Attributes: map[string]string{"manifest": o.Path}})
			edges = append(edges, deps.Edge{From: root, To: id})
		}
	}
	type acc struct {
		node    deps.Node
		fws     map[string]bool
		modules map[string]string
	}
	nodes := map[string]*acc{}
	for _, o := range owned {
		a := o.Assets
		owner := root
		if multi {
			owner = moduleOf[o.Path]
		}
		// memberModule maps a project library to the module of a member.
		memberModule := func(key string) (string, bool) {
			lib, ok := a.Libraries[key]
			if !ok || !multi || !strings.EqualFold(lib.Type, "project") || lib.Path == "" {
				return "", false
			}
			pf, ok := Resolve(path.Dir(o.Path), normalize(lib.Path))
			if !ok {
				return "", false
			}
			id, ok := moduleOf[pf]
			return id, ok
		}
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
					n = &acc{node: deps.Node{ID: id, Ecosystem: deps.NuGet, Name: name, Version: ver, Scope: deps.ScopeCompile},
						fws: map[string]bool{}, modules: map[string]string{}}
					nodes[id] = n
				}
				n.fws[fw] = true
				if multi {
					n.modules[owner] = deps.ScopeCompile
				}
			}
			link := func(from string, depName string) {
				key, ok := byName[strings.ToLower(depName)]
				if !ok || !isPackage(key) {
					return
				}
				edges = append(edges, deps.Edge{From: from, To: nodeID(key)})
			}
			for key, lib := range libs {
				from := owner
				switch {
				case isPackage(key):
					from = nodeID(key)
				case kind[key] != "project":
					continue
				default:
					if id, ok := memberModule(key); ok {
						if id != owner {
							edges = append(edges, deps.Edge{From: owner, To: id})
						}
						from = id
					}
				}
				for dep := range lib.Dependencies {
					link(from, dep)
				}
			}
			if f, ok := a.Project.Frameworks[fw]; ok {
				for dep, d := range f.Dependencies {
					if d.Target == "" || strings.EqualFold(d.Target, "Package") {
						link(owner, dep)
					}
				}
			}
		}
	}
	for _, n := range nodes {
		fws := make([]string, 0, len(n.fws))
		for f := range n.fws {
			fws = append(fws, f)
		}
		sort.Strings(fws)
		n.node.Attributes = map[string]string{"targetFrameworks": strings.Join(fws, ",")}
		if len(n.modules) > 0 {
			n.node.Modules = n.modules
		}
		list = append(list, n.node)
	}
	return deps.NewGraph(root, source, list, edges)
}
