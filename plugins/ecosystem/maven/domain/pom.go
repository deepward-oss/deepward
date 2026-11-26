// Package domain contains the pure Maven model: POM parsing with byte spans
// for surgical edits, property resolution and dependency-tree parsing.
package domain

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Span is a byte range [Start, End) in the source file.
type Span struct{ Start, End int }

// Value is a text value and where it is in the file.
type Value struct {
	Text string
	Span Span
}

// Dependency is a <dependency> element.
type Dependency struct {
	GroupID, ArtifactID, Version Value
	Scope, Type, Classifier      string
	// Element is the span of the whole <dependency> element.
	Element Span
}

// Coordinates returns "group:artifact".
func (d Dependency) Coordinates() string { return d.GroupID.Text + ":" + d.ArtifactID.Text }

// Parent is the <parent> element.
type Parent struct {
	GroupID, ArtifactID, Version Value
	RelativePath                 string
	HasRelativePath              bool
}

// POM is the subset of a Maven POM deepward needs.
type POM struct {
	GroupID, ArtifactID, Version Value
	Parent                       *Parent
	Modules                      []string
	Properties                   map[string]Value
	Dependencies                 []Dependency
	Managed                      []Dependency
	// ManagedDependenciesEnd is the offset of "</dependencies>" inside
	// <dependencyManagement> (-1 if absent).
	ManagedDependenciesEnd int
	// ProjectEnd is the offset of "</project>".
	ProjectEnd int
	// Indent is the indentation unit detected in the file (default two spaces).
	Indent string
}

// EffectiveGroupID returns the group ID, inherited from the parent if absent.
func (p *POM) EffectiveGroupID() string {
	if p.GroupID.Text == "" && p.Parent != nil {
		return p.Parent.GroupID.Text
	}
	return p.GroupID.Text
}

// EffectiveVersion returns the version, inherited from the parent if absent.
func (p *POM) EffectiveVersion() string {
	if p.Version.Text == "" && p.Parent != nil {
		return p.Parent.Version.Text
	}
	return p.Version.Text
}

// ParsePOM parses a POM and records byte spans of the values it extracts.
func ParsePOM(data []byte) (*POM, error) {
	pom := &POM{Properties: map[string]Value{}, ManagedDependenciesEnd: -1, ProjectEnd: -1, Indent: detectIndent(data)}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var stack []string
	var dep *Dependency
	var depStart int
	inManaged := false
	for {
		before := int(dec.InputOffset())
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid POM: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			path := strings.Join(stack, "/")
			switch path {
			case "project/dependencies/dependency", "project/dependencyManagement/dependencies/dependency":
				dep, depStart = &Dependency{}, before
				inManaged = strings.HasPrefix(path, "project/dependencyManagement")
			case "project/parent":
				pom.Parent = &Parent{}
			case "project/parent/relativePath":
				if pom.Parent != nil {
					pom.Parent.HasRelativePath = true
				}
			}
		case xml.EndElement:
			path := strings.Join(stack, "/")
			switch path {
			case "project/dependencies/dependency", "project/dependencyManagement/dependencies/dependency":
				if dep != nil {
					dep.Element = Span{depStart, int(dec.InputOffset())}
					if inManaged {
						pom.Managed = append(pom.Managed, *dep)
					} else {
						pom.Dependencies = append(pom.Dependencies, *dep)
					}
				}
				dep = nil
			case "project/dependencyManagement/dependencies":
				pom.ManagedDependenciesEnd = before
			case "project":
				pom.ProjectEnd = before
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			raw := data[before:int(dec.InputOffset())]
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			lead := len(raw) - len(bytes.TrimLeft(raw, " \t\r\n"))
			v := Value{Text: text, Span: Span{before + lead, before + lead + len(bytes.TrimSpace(raw))}}
			assign(pom, dep, stack, v)
		}
	}
	if pom.ProjectEnd < 0 {
		return nil, errors.New("invalid POM: no </project> element")
	}
	return pom, nil
}

func assign(pom *POM, dep *Dependency, stack []string, v Value) {
	path := strings.Join(stack, "/")
	last := stack[len(stack)-1]
	switch {
	case path == "project/groupId":
		pom.GroupID = v
	case path == "project/artifactId":
		pom.ArtifactID = v
	case path == "project/version":
		pom.Version = v
	case path == "project/parent/groupId" && pom.Parent != nil:
		pom.Parent.GroupID = v
	case path == "project/parent/artifactId" && pom.Parent != nil:
		pom.Parent.ArtifactID = v
	case path == "project/parent/version" && pom.Parent != nil:
		pom.Parent.Version = v
	case path == "project/parent/relativePath" && pom.Parent != nil:
		pom.Parent.RelativePath, pom.Parent.HasRelativePath = v.Text, true
	case path == "project/modules/module" || path == "project/profiles/profile/modules/module":
		pom.Modules = append(pom.Modules, v.Text)
	case len(stack) == 3 && stack[1] == "properties":
		pom.Properties[last] = v
	case dep != nil && (len(stack) == 4 && stack[1] == "dependencies" || len(stack) == 5 && stack[1] == "dependencyManagement"):
		switch last {
		case "groupId":
			dep.GroupID = v
		case "artifactId":
			dep.ArtifactID = v
		case "version":
			dep.Version = v
		case "scope":
			dep.Scope = v.Text
		case "type":
			dep.Type = v.Text
		case "classifier":
			dep.Classifier = v.Text
		}
	}
}

var indentPattern = regexp.MustCompile(`(?m)^([ \t]+)<`)

func detectIndent(data []byte) string {
	best := ""
	for _, m := range indentPattern.FindAllSubmatch(data, 50) {
		ind := string(m[1])
		if best == "" || len(ind) < len(best) {
			best = ind
		}
	}
	if best == "" {
		return "  "
	}
	return best
}

var propRef = regexp.MustCompile(`^\$\{([^}]+)\}$`)

// PropertyRef returns the property name if v is exactly "${name}".
func PropertyRef(v string) (string, bool) {
	m := propRef.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", false
	}
	return m[1], true
}
