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
// one project graph. Modules of the build itself are not nodes: their
// direct dependencies become direct dependencies of the root.
func BuildGraph(root, source string, reports []ProjectReport) (*deps.Graph, error) {
	build := map[string]bool{}
	for _, r := range reports {
		build[buildKey(r.Module)] = true
	}
	type acc struct {
		node     deps.Node
		projects map[string]bool
		scalas   map[string]bool
	}
	nodes := map[string]*acc{}
	var edges []deps.Edge
	idOf := func(id string) (string, bool) {
		org, name, rev, ok := splitID(id)
		if !ok || rev == "" {
			return "", false
		}
		return purl.New(purl.TypeMaven, org, name, rev).String(), true
	}
	for _, r := range reports {
		for _, c := range r.Configurations {
			scope, ok := scopeOf[c.Name]
			if !ok {
				continue
			}
			for _, m := range c.Modules {
				if m.Evicted || build[buildKey(m.ID)] {
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
						projects: map[string]bool{}, scalas: map[string]bool{}}
					nodes[id] = a
				} else if scopeRank[scope] > scopeRank[a.node.Scope] {
					a.node.Scope = scope
				}
				if r.ScalaBinaryVersion != "" {
					a.scalas[r.ScalaBinaryVersion] = true
				}
				for _, caller := range m.Callers {
					if build[buildKey(caller)] {
						a.projects[r.Project] = true
						edges = append(edges, deps.Edge{From: root, To: id})
						continue
					}
					if from, ok := idOf(caller); ok {
						edges = append(edges, deps.Edge{From: from, To: id})
					}
				}
			}
		}
	}
	list := make([]deps.Node, 0, len(nodes))
	known := map[string]bool{root: true}
	for id, a := range nodes {
		known[id] = true
		attrs := map[string]string{}
		if len(a.projects) > 0 {
			attrs["projects"] = strings.Join(sortedSet(a.projects), ",")
		}
		if len(a.scalas) > 0 {
			attrs["scalaBinaryVersions"] = strings.Join(sortedSet(a.scalas), ",")
		}
		a.node.Attributes = attrs
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
