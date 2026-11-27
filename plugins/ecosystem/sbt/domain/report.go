// Package domain turns the JSON lines written by the injected sbt plugin into
// a dependency graph.
package domain

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// Module is one resolved module of a configuration.
type Module struct {
	ID      string   `json:"id"`
	Evicted bool     `json:"evicted"`
	Callers []string `json:"callers"`
}

// Configuration is one sbt configuration report.
type Configuration struct {
	Name    string   `json:"name"`
	Modules []Module `json:"modules"`
}

// ProjectReport is one line written by deepwardGraph (per subproject and
// Scala version).
type ProjectReport struct {
	Project            string          `json:"project"`
	Module             string          `json:"module"`
	ScalaVersion       string          `json:"scalaVersion"`
	ScalaBinaryVersion string          `json:"scalaBinaryVersion"`
	Configurations     []Configuration `json:"configurations"`
}

// ParseReports reads JSON lines.
func ParseReports(r io.Reader) ([]ProjectReport, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	var out []ProjectReport
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var pr ProjectReport
		if err := json.Unmarshal([]byte(text), &pr); err != nil {
			return nil, fmt.Errorf("invalid sbt graph output at line %d: %w", line, err)
		}
		out = append(out, pr)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("sbt wrote no dependency graph")
	}
	return out, nil
}

// splitID splits "org:name:rev".
func splitID(id string) (org, name, rev string, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// buildKey identifies a module of the build ignoring the Scala suffix.
func buildKey(id string) string {
	org, name, _, ok := splitID(id)
	if !ok {
		return id
	}
	base, _ := purl.SplitScala(name)
	return org + ":" + base
}

var scopeOf = map[string]string{"compile": deps.ScopeCompile, "runtime": deps.ScopeRuntime, "test": deps.ScopeTest,
	"provided": deps.ScopeProvided, "optional": "optional"}

var scopeRank = map[string]int{deps.ScopeCompile: 6, deps.ScopeRuntime: 5, deps.ScopeProvided: 4, "optional": 3, deps.ScopeTest: 1}

// BuildGraph merges the reports of all subprojects (and Scala versions) into
// one project graph. With more than one subproject, every subproject is a
// module node (identified and named by its sbt project ID) that points at
// the libraries it declares and at the subprojects it depends on, and every
// library records its scope in each subproject whose report contains it.
// A build with one subproject keeps a flat graph whose root points at the
// direct dependencies.
func BuildGraph(root, source string, reports []ProjectReport) (*deps.Graph, error) {
	projectOf := map[string]string{}
	for _, r := range reports {
		projectOf[buildKey(r.Module)] = r.Project
	}
	projects := map[string]bool{}
	for _, r := range reports {
		projects[r.Project] = true
	}
	multi := len(projects) > 1
	// declarer is the node that declares the dependencies of a build module.
	declarer := func(project string) string {
		if multi {
			return deps.ModuleID(project)
		}
		return root
	}
	type acc struct {
		node   deps.Node
		scopes map[string]string
		scalas map[string]bool
	}
	nodes := map[string]*acc{}
	var edges []deps.Edge
	var list []deps.Node
	if multi {
		for _, pr := range sortedSet(projects) {
			list = append(list, deps.Node{ID: deps.ModuleID(pr), Ecosystem: deps.Maven, Name: pr, Kind: deps.KindModule})
			edges = append(edges, deps.Edge{From: root, To: deps.ModuleID(pr)})
		}
	}
	idOf := func(id string) (string, bool) {
		org, name, rev, ok := splitID(id)
		if !ok || rev == "" {
			return "", false
		}
		return purl.New(purl.TypeMaven, org, name, rev).String(), true
	}
	widest := func(a, b string) string {
		if scopeRank[b] > scopeRank[a] {
			return b
		}
		return a
	}
	for _, r := range reports {
		for _, c := range r.Configurations {
			scope, ok := scopeOf[c.Name]
			if !ok {
				continue
			}
			for _, m := range c.Modules {
				if m.Evicted {
					continue
				}
				if dep, isBuild := projectOf[buildKey(m.ID)]; isBuild {
					// A subproject this one depends on (dependsOn).
					for _, caller := range m.Callers {
						if from, ok := projectOf[buildKey(caller)]; ok && multi && from != dep {
							edges = append(edges, deps.Edge{From: deps.ModuleID(from), To: deps.ModuleID(dep)})
						}
					}
					continue
				}
				id, ok := idOf(m.ID)
				if !ok {
					continue
				}
				org, name, rev, _ := splitID(m.ID)
				a := nodes[id]
				if a == nil {
					a = &acc{node: deps.Node{ID: id, Ecosystem: deps.Maven, Name: org + ":" + name, Version: rev, Scope: scope},
						scopes: map[string]string{}, scalas: map[string]bool{}}
					nodes[id] = a
				} else {
					a.node.Scope = widest(a.node.Scope, scope)
				}
				if multi {
					a.scopes[deps.ModuleID(r.Project)] = widest(a.scopes[deps.ModuleID(r.Project)], scope)
				}
				if r.ScalaBinaryVersion != "" {
					a.scalas[r.ScalaBinaryVersion] = true
				}
				for _, caller := range m.Callers {
					if from, isBuild := projectOf[buildKey(caller)]; isBuild {
						edges = append(edges, deps.Edge{From: declarer(from), To: id})
						continue
					}
					if from, ok := idOf(caller); ok {
						edges = append(edges, deps.Edge{From: from, To: id})
					}
				}
			}
		}
	}
	known := map[string]bool{root: true}
	for _, n := range list {
		known[n.ID] = true
	}
	for id, a := range nodes {
		known[id] = true
		if len(a.scalas) > 0 {
			a.node.Attributes = map[string]string{"scalaBinaryVersions": strings.Join(sortedSet(a.scalas), ",")}
		}
		if len(a.scopes) > 0 {
			a.node.Modules = a.scopes
		}
		list = append(list, a.node)
	}
	// Callers can be evicted modules that are not nodes; drop such edges.
	kept := edges[:0]
	for _, e := range edges {
		if known[e.From] && known[e.To] {
			kept = append(kept, e)
		}
	}
	return deps.NewGraph(root, source, list, kept)
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
