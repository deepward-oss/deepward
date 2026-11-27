package domain

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"
)

// ProjectFileExts are the extensions of .NET project files.
var ProjectFileExts = map[string]bool{".csproj": true, ".fsproj": true, ".vbproj": true}

// IsProjectFile reports whether a path names a .NET project file.
func IsProjectFile(p string) bool { return ProjectFileExts[strings.ToLower(path.Ext(p))] }

// normalize turns a Windows or Unix relative path into a clean slash path.
func normalize(p string) string {
	return path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
}

var slnProjectRe = regexp.MustCompile(`(?m)^\s*Project\("\{[^}]*\}"\)\s*=\s*"[^"]*"\s*,\s*"([^"]+)"`)

// SolutionProjects returns the project files a .sln file lists, as clean
// slash paths relative to the solution's directory. Solution folders and
// other non-project entries are skipped.
func SolutionProjects(data []byte) []string {
	var out []string
	for _, m := range slnProjectRe.FindAllSubmatch(data, -1) {
		if p := normalize(string(m[1])); IsProjectFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// SlnxProjects returns the project files a .slnx file lists (Project
// elements at any depth, for example inside Folder elements), relative to
// the solution's directory.
func SlnxProjects(data []byte) ([]string, error) {
	return attrValues(data, "Project", "Path", true)
}

// ProjectReferences returns the Include paths of the ProjectReference items
// of a project file, relative to the project file's directory.
func ProjectReferences(data []byte) ([]string, error) {
	return attrValues(data, "ProjectReference", "Include", false)
}

// attrValues collects the attribute attr of every element named elem.
func attrValues(data []byte, elem, attr string, onlyProjects bool) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var out []string
	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != elem {
			continue
		}
		for _, a := range se.Attr {
			if !strings.EqualFold(a.Name.Local, attr) || strings.TrimSpace(a.Value) == "" {
				continue
			}
			p := normalize(a.Value)
			if !onlyProjects || IsProjectFile(p) {
				out = append(out, p)
			}
		}
	}
}

// Resolve joins a path relative to dir (both slash paths from the workspace
// root) and reports whether the result stays inside the workspace.
func Resolve(dir, rel string) (string, bool) {
	p := path.Clean(path.Join(dir, rel))
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return "", false
	}
	return p, true
}
