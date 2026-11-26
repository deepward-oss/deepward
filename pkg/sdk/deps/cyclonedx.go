package deps

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

type cdxComponent struct {
	BOMRef  string `json:"bom-ref"`
	Type    string `json:"type"`
	Group   string `json:"group"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl"`
	Scope   string `json:"scope"`
	// Nested components (assemblies) are flattened.
	Components []cdxComponent `json:"components"`
}

type cdxBOM struct {
	BOMFormat   string `json:"bomFormat"`
	SpecVersion string `json:"specVersion"`
	Metadata    struct {
		Component *cdxComponent `json:"component"`
	} `json:"metadata"`
	Components   []cdxComponent `json:"components"`
	Dependencies []struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	} `json:"dependencies"`
}

// ParseCycloneDX reads a CycloneDX JSON SBOM (spec 1.4 or later) into a
// dependency graph. Components without a supported purl are ignored. If the
// SBOM has no dependency information, every component is treated as direct.
func ParseCycloneDX(r io.Reader, source string) (*Graph, error) {
	var bom cdxBOM
	if err := json.NewDecoder(r).Decode(&bom); err != nil {
		return nil, fmt.Errorf("invalid CycloneDX JSON: %w", err)
	}
	if bom.BOMFormat != "CycloneDX" {
		return nil, fmt.Errorf("not a CycloneDX BOM (bomFormat %q)", bom.BOMFormat)
	}
	if !specAtLeast(bom.SpecVersion, 1, 4) {
		return nil, fmt.Errorf("CycloneDX spec %q is not supported (need 1.4 or later)", bom.SpecVersion)
	}
	rootRef := "root"
	rootName := "root"
	if c := bom.Metadata.Component; c != nil {
		rootName = firstOf(c.Name, rootName)
		rootRef = firstOf(c.BOMRef, c.PURL, rootRef)
	}
	root := "project:" + rootName
	ids := map[string]string{rootRef: root}
	var nodes []Node
	var flat []cdxComponent
	var walk func([]cdxComponent)
	walk = func(cs []cdxComponent) {
		for _, c := range cs {
			flat = append(flat, c)
			walk(c.Components)
		}
	}
	walk(bom.Components)
	for _, c := range flat {
		if c.PURL == "" {
			continue
		}
		p, err := purl.Parse(c.PURL)
		if err != nil {
			continue
		}
		eco, err := EcosystemOf(p.Type)
		if err != nil {
			continue
		}
		// Normalize: drop qualifiers such as ?type=jar.
		p.Qualifiers, p.Subpath = nil, ""
		if p.Version == "" {
			p.Version = c.Version
		}
		id := p.String()
		ref := firstOf(c.BOMRef, c.PURL)
		ids[ref] = id
		nodes = append(nodes, Node{ID: id, Ecosystem: eco, Name: p.Coordinates(), Version: p.Version, Scope: cdxScope(c.Scope)})
	}
	var edges []Edge
	hasDeps := false
	for _, d := range bom.Dependencies {
		from, ok := ids[d.Ref]
		if !ok {
			continue
		}
		for _, to := range d.DependsOn {
			if target, ok := ids[to]; ok {
				edges = append(edges, Edge{From: from, To: target})
				hasDeps = true
			}
		}
	}
	if !hasDeps {
		for _, n := range nodes {
			edges = append(edges, Edge{From: root, To: n.ID})
		}
	}
	return NewGraph(root, source, nodes, edges)
}

func cdxScope(s string) string {
	switch s {
	case "excluded":
		return ScopeTest
	case "optional":
		return "optional"
	}
	return ScopeCompile
}

func specAtLeast(v string, major, minor int) bool {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || (ma == major && mi >= minor)
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
