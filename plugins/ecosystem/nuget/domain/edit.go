package domain

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// File is an MSBuild file (project file or props).
type File struct {
	Path string
	Data []byte
}

// span is a value position.
type span struct {
	file       string
	start, end int
	text       string
}

// item is a PackageReference or PackageVersion element.
type item struct {
	kind     string // PackageReference or PackageVersion
	id       string
	version  *span
	override *span
}

// model is what the editor needs from all files.
type model struct {
	items      []item
	props      map[string]span
	central    bool
	pinning    bool
	itemGroups map[string]map[string]int // file -> kind -> offset of "</ItemGroup>" of the first group with that kind
	projectEnd map[string]int
	nl         map[string]string
	data       map[string][]byte
}

var attrRe = regexp.MustCompile(`(?i)\b(Include|Update|Version|VersionOverride)\s*=\s*"([^"]*)"`)

func scan(files []File) (*model, error) {
	m := &model{props: map[string]span{}, itemGroups: map[string]map[string]int{}, projectEnd: map[string]int{}, nl: map[string]string{}, data: map[string][]byte{}}
	for _, f := range files {
		m.data[f.Path] = f.Data
		m.nl[f.Path] = "\n"
		if bytes.Contains(f.Data, []byte("\r\n")) {
			m.nl[f.Path] = "\r\n"
		}
		dec := xml.NewDecoder(bytes.NewReader(f.Data))
		dec.Strict = false
		var stack []string
		var cur *item
		groupKinds := map[string]bool{}
		for {
			before := int(dec.InputOffset())
			tok, err := dec.RawToken()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("%s: invalid XML: %w", f.Path, err)
			}
			switch t := tok.(type) {
			case xml.StartElement:
				stack = append(stack, t.Name.Local)
				name := t.Name.Local
				if name == "PackageReference" || name == "PackageVersion" {
					raw := f.Data[before:int(dec.InputOffset())]
					it := item{kind: name}
					for _, mt := range attrRe.FindAllSubmatchIndex(raw, -1) {
						key := strings.ToLower(string(raw[mt[2]:mt[3]]))
						s := span{file: f.Path, start: before + mt[4], end: before + mt[5], text: string(raw[mt[4]:mt[5]])}
						switch key {
						case "include", "update":
							if it.id == "" {
								it.id = s.text
							}
						case "version":
							it.version = &s
						case "versionoverride":
							it.override = &s
						}
					}
					groupKinds[name] = true
					m.items = append(m.items, it)
					if !bytes.HasSuffix(bytes.TrimRight(raw, " \t\r\n"), []byte("/>")) {
						cur = &m.items[len(m.items)-1]
					}
				}
				if name == "ItemGroup" {
					groupKinds = map[string]bool{}
				}
			case xml.EndElement:
				name := t.Name.Local
				switch name {
				case "ItemGroup":
					if m.itemGroups[f.Path] == nil {
						m.itemGroups[f.Path] = map[string]int{}
					}
					for k := range groupKinds {
						if _, ok := m.itemGroups[f.Path][k]; !ok {
							m.itemGroups[f.Path][k] = before
						}
					}
				case "PackageReference", "PackageVersion":
					cur = nil
				case "Project":
					m.projectEnd[f.Path] = before
				}
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case xml.CharData:
				text := strings.TrimSpace(string(t))
				if text == "" || len(stack) < 2 {
					continue
				}
				raw := f.Data[before:int(dec.InputOffset())]
				lead := len(raw) - len(bytes.TrimLeft(raw, " \t\r\n"))
				s := span{file: f.Path, start: before + lead, end: before + lead + len(bytes.TrimSpace(raw)), text: text}
				parent, last := stack[len(stack)-2], stack[len(stack)-1]
				switch {
				case cur != nil && last == "Version":
					cur.version = &s
				case cur != nil && last == "VersionOverride":
					cur.override = &s
				case parent == "PropertyGroup":
					m.props[last] = s
					switch last {
					case "ManagePackageVersionsCentrally":
						m.central = strings.EqualFold(text, "true")
					case "CentralPackageTransitivePinningEnabled":
						m.pinning = strings.EqualFold(text, "true")
					}
				}
			}
		}
	}
	return m, nil
}

var propRef = regexp.MustCompile(`^\$\(([A-Za-z_][A-Za-z0-9_.\-]*)\)$`)

// resolve returns the span to edit for a version value.
func (m *model) resolve(s span) (span, string) {
	for depth := 0; depth < 10; depth++ {
		mt := propRef.FindStringSubmatch(strings.TrimSpace(s.text))
		if mt == nil {
			if strings.ContainsAny(s.text, "$[(,*") {
				return span{}, fmt.Sprintf("the version %q is an expression or a range", s.text)
			}
			return s, ""
		}
		def, ok := m.props[mt[1]]
		if !ok {
			return span{}, fmt.Sprintf("property $(%s) is not defined in the repository files", mt[1])
		}
		s = def
	}
	return span{}, "property references are nested too deeply"
}

func edit(s span, to string) sdk.TextEdit {
	return sdk.TextEdit{Path: s.file, Start: s.start, End: s.end, Replacement: to, Expected: s.text}
}

// Plan computes the edits that set package id to change.To. projectFiles are
// the project's .csproj/.fsproj/.vbproj paths; props is the
// Directory.Packages.props path ("" if none).
func Plan(files []File, projectFiles []string, props string, change sdk.PackageChange, id string) ([]sdk.TextEdit, *sdk.ManualChange) {
	manual := func(reason string) ([]sdk.TextEdit, *sdk.ManualChange) {
		return nil, &sdk.ManualChange{Change: change, Reason: reason}
	}
	m, err := scan(files)
	if err != nil {
		return manual(err.Error())
	}
	var edits []sdk.TextEdit
	var versionless bool
	var central *item
	for i := range m.items {
		it := &m.items[i]
		if !strings.EqualFold(it.id, id) {
			continue
		}
		if it.kind == "PackageVersion" {
			central = it
			continue
		}
		target := it.override
		if target == nil {
			target = it.version
		}
		if target == nil {
			versionless = true
			continue
		}
		s, reason := m.resolve(*target)
		if reason != "" {
			return manual(reason)
		}
		edits = append(edits, edit(s, change.To))
	}
	if len(edits) > 0 {
		return edits, nil
	}
	if central != nil && central.version != nil {
		s, reason := m.resolve(*central.version)
		if reason != "" {
			return manual(reason)
		}
		return []sdk.TextEdit{edit(s, change.To)}, nil
	}
	if versionless && !change.Pin {
		return manual("the version is managed outside the repository files")
	}
	if !change.Pin {
		return manual(fmt.Sprintf("%s is not referenced by the project", id))
	}
	return m.pin(projectFiles, props, change, id)
}

func (m *model) pin(projectFiles []string, props string, change sdk.PackageChange, id string) ([]sdk.TextEdit, *sdk.ManualChange) {
	reason := strings.ReplaceAll(firstNonEmpty(change.Reason, "fixes a vulnerable transitive dependency"), "--", "-")
	comment := "<!-- Deepward: pinned transitive dependency: " + reason + " -->"
	var edits []sdk.TextEdit
	targets := pinTargets(projectFiles, change.Targets)
	if m.central && props != "" {
		edits = append(edits, m.insert(props, "PackageVersion", comment, fmt.Sprintf(`<PackageVersion Include="%s" Version="%s" />`, id, change.To)))
		if m.pinning {
			return edits, nil
		}
		if len(targets) == 0 {
			return nil, &sdk.ManualChange{Change: change, Reason: "no project file to reference the pinned package"}
		}
		for _, f := range targets {
			edits = append(edits, m.insert(f, "PackageReference", comment, fmt.Sprintf(`<PackageReference Include="%s" />`, id)))
		}
		return edits, nil
	}
	if len(targets) == 0 {
		return nil, &sdk.ManualChange{Change: change, Reason: "no project file to pin the package in"}
	}
	for _, f := range targets {
		edits = append(edits, m.insert(f, "PackageReference", comment, fmt.Sprintf(`<PackageReference Include="%s" Version="%s" />`, id, change.To)))
	}
	return edits, nil
}

// pinTargets returns the project files a pin goes to: the change's targets
// that are project files of the project (the modules the package enters
// through), else the first project file.
func pinTargets(projectFiles, targets []string) []string {
	var out []string
	for _, t := range targets {
		for _, f := range projectFiles {
			if f == t {
				out = append(out, f)
				break
			}
		}
	}
	if len(out) == 0 && len(projectFiles) > 0 {
		out = projectFiles[:1]
	}
	return out
}

// insert adds an element to the first ItemGroup holding elements of kind,
// or a new ItemGroup before </Project>, matching the file's indentation.
func (m *model) insert(file, kind, comment, element string) sdk.TextEdit {
	nl, data := m.nl[file], m.data[file]
	if off, ok := m.itemGroups[file][kind]; ok {
		at := lineStart(data, off)
		ind := indentation(data, at) + "  "
		return sdk.TextEdit{Path: file, Start: at, End: at, Replacement: ind + comment + nl + ind + element + nl}
	}
	at := lineStart(data, m.projectEnd[file])
	return sdk.TextEdit{Path: file, Start: at, End: at,
		Replacement: "  <ItemGroup>" + nl + "    " + comment + nl + "    " + element + nl + "  </ItemGroup>" + nl}
}

func lineStart(data []byte, off int) int {
	if off > len(data) {
		off = len(data)
	}
	return bytes.LastIndexByte(data[:off], '\n') + 1
}

func indentation(data []byte, at int) string {
	e := at
	for e < len(data) && (data[e] == ' ' || data[e] == '\t') {
		e++
	}
	return string(data[at:e])
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
