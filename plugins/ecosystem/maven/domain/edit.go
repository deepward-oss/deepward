package domain

import (
	"fmt"
	"path"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// File is a POM of the project with its content.
type File struct {
	Path string // slash path relative to the repository root
	Data []byte
	POM  *POM
}

// Plan computes the edits that set group:artifact to change.To in the
// project's POMs. root is the path of the reactor root POM (used for pins).
// When the change cannot be applied safely it returns a ManualChange.
func Plan(files []File, root string, change sdk.PackageChange, group, artifact string) ([]sdk.TextEdit, *sdk.ManualChange) {
	byPath := map[string]*File{}
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	var edits []sdk.TextEdit
	seen := map[string]bool{}
	add := func(e sdk.TextEdit) {
		key := fmt.Sprintf("%s:%d:%d", e.Path, e.Start, e.End)
		if !seen[key] {
			seen[key] = true
			edits = append(edits, e)
		}
	}
	manual := func(reason string) *sdk.ManualChange {
		return &sdk.ManualChange{Change: change, Reason: reason}
	}
	var declared, unversioned bool
	for _, f := range files {
		if f.POM == nil {
			continue
		}
		for _, d := range append(append([]Dependency{}, f.POM.Dependencies...), f.POM.Managed...) {
			if d.GroupID.Text != group || d.ArtifactID.Text != artifact {
				continue
			}
			declared = true
			if d.Version.Text == "" {
				unversioned = true
				continue
			}
			e, reason := versionEdit(byPath, f.Path, d.Version, change.To)
			if reason != "" {
				return nil, manual(reason)
			}
			add(e)
		}
	}
	switch {
	case len(edits) > 0:
		return edits, nil
	case change.Pin:
		return pin(byPath, root, change, group, artifact)
	case unversioned:
		return nil, manual("the version is managed outside the repository (parent POM or imported BOM)")
	case declared:
		return nil, manual("the dependency has no version to change")
	}
	return nil, manual(fmt.Sprintf("%s:%s is not declared in the project's POMs", group, artifact))
}

// versionEdit edits a literal version or the property it references.
func versionEdit(byPath map[string]*File, file string, v Value, to string) (sdk.TextEdit, string) {
	for depth := 0; depth < 10; depth++ {
		name, isRef := PropertyRef(v.Text)
		if !isRef {
			if strings.Contains(v.Text, "${") {
				return sdk.TextEdit{}, fmt.Sprintf("the version %q is a computed expression", v.Text)
			}
			return sdk.TextEdit{Path: file, Start: v.Span.Start, End: v.Span.End, Replacement: to, Expected: v.Text}, ""
		}
		if name == "project.version" || strings.HasPrefix(name, "project.") {
			return sdk.TextEdit{}, fmt.Sprintf("the version refers to ${%s}", name)
		}
		def, defFile, ok := findProperty(byPath, file, name)
		if !ok {
			return sdk.TextEdit{}, fmt.Sprintf("property ${%s} is defined outside the repository", name)
		}
		v, file = def, defFile
	}
	return sdk.TextEdit{}, "property references are nested too deeply"
}

// findProperty looks for a property in the POM and its parents within the
// repository.
func findProperty(byPath map[string]*File, file, name string) (Value, string, bool) {
	visited := map[string]bool{}
	for cur := file; cur != "" && !visited[cur]; {
		visited[cur] = true
		f, ok := byPath[cur]
		if !ok || f.POM == nil {
			return Value{}, "", false
		}
		if v, ok := f.POM.Properties[name]; ok {
			return v, cur, true
		}
		cur = parentPath(byPath, cur, f.POM)
	}
	return Value{}, "", false
}

func parentPath(byPath map[string]*File, file string, pom *POM) string {
	if pom.Parent == nil {
		return ""
	}
	rel := "../pom.xml"
	if pom.Parent.HasRelativePath {
		rel = pom.Parent.RelativePath
		if rel == "" {
			return ""
		}
	}
	p := path.Join(path.Dir(file), rel)
	if !strings.HasSuffix(p, ".xml") {
		p = path.Join(p, "pom.xml")
	}
	if f, ok := byPath[p]; ok && f.POM != nil && f.POM.ArtifactID.Text == pom.Parent.ArtifactID.Text {
		return p
	}
	return ""
}

// pin adds a dependencyManagement entry to the root POM.
func pin(byPath map[string]*File, root string, change sdk.PackageChange, group, artifact string) ([]sdk.TextEdit, *sdk.ManualChange) {
	f, ok := byPath[root]
	if !ok || f.POM == nil {
		return nil, &sdk.ManualChange{Change: change, Reason: "the root POM could not be parsed"}
	}
	nl := "\n"
	if strings.Contains(string(f.Data), "\r\n") {
		nl = "\r\n"
	}
	unit := f.POM.Indent
	comment := strings.ReplaceAll(firstNonEmpty(change.Reason, "fixes a vulnerable transitive dependency"), "--", "-")
	entry := func(ind string) string {
		return ind + "<!-- Deepward: pinned transitive dependency: " + comment + " -->" + nl +
			ind + "<dependency>" + nl +
			ind + unit + "<groupId>" + group + "</groupId>" + nl +
			ind + unit + "<artifactId>" + artifact + "</artifactId>" + nl +
			ind + unit + "<version>" + change.To + "</version>" + nl +
			ind + "</dependency>" + nl
	}
	if end := f.POM.ManagedDependenciesEnd; end >= 0 {
		at := lineStart(f.Data, end)
		ind := indentation(f.Data, end) + unit
		return []sdk.TextEdit{{Path: root, Start: at, End: at, Replacement: entry(ind)}}, nil
	}
	at := lineStart(f.Data, f.POM.ProjectEnd)
	base := indentation(f.Data, f.POM.ProjectEnd)
	text := base + unit + "<dependencyManagement>" + nl + base + unit + unit + "<dependencies>" + nl +
		entry(base+unit+unit+unit) + base + unit + unit + "</dependencies>" + nl + base + unit + "</dependencyManagement>" + nl
	return []sdk.TextEdit{{Path: root, Start: at, End: at, Replacement: text}}, nil
}

func lineStart(data []byte, off int) int {
	for i := off - 1; i >= 0; i-- {
		if data[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func indentation(data []byte, off int) string {
	s := lineStart(data, off)
	e := s
	for e < len(data) && (data[e] == ' ' || data[e] == '\t') {
		e++
	}
	return string(data[s:e])
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
