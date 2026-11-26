package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// jsonValue is a parsed JSON value with byte offsets into the source.
type jsonValue struct {
	start, end int
	kind       byte // '{', '[', '"', or 'v' for other scalars
	members    []jsonMember
}

type jsonMember struct {
	key      string
	keyStart int
	value    *jsonValue
}

type jsonScanner struct {
	data []byte
	pos  int
}

func (s *jsonScanner) ws() {
	for s.pos < len(s.data) && strings.IndexByte(" \t\r\n", s.data[s.pos]) >= 0 {
		s.pos++
	}
}

func (s *jsonScanner) fail(what string) error {
	return fmt.Errorf("invalid JSON at byte %d: %s", s.pos, what)
}

func (s *jsonScanner) str() (string, error) {
	start := s.pos
	s.pos++
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case '\\':
			s.pos += 2
			continue
		case '"':
			s.pos++
			var out string
			if err := json.Unmarshal(s.data[start:s.pos], &out); err != nil {
				return "", s.fail("bad string")
			}
			return out, nil
		}
		s.pos++
	}
	return "", s.fail("unterminated string")
}

func (s *jsonScanner) value() (*jsonValue, error) {
	s.ws()
	if s.pos >= len(s.data) {
		return nil, s.fail("unexpected end")
	}
	v := &jsonValue{start: s.pos}
	switch c := s.data[s.pos]; c {
	case '{':
		v.kind = '{'
		s.pos++
		s.ws()
		if s.pos < len(s.data) && s.data[s.pos] == '}' {
			s.pos++
			v.end = s.pos
			return v, nil
		}
		for {
			s.ws()
			if s.pos >= len(s.data) || s.data[s.pos] != '"' {
				return nil, s.fail("expected a key")
			}
			keyStart := s.pos
			key, err := s.str()
			if err != nil {
				return nil, err
			}
			s.ws()
			if s.pos >= len(s.data) || s.data[s.pos] != ':' {
				return nil, s.fail("expected ':'")
			}
			s.pos++
			mv, err := s.value()
			if err != nil {
				return nil, err
			}
			v.members = append(v.members, jsonMember{key: key, keyStart: keyStart, value: mv})
			s.ws()
			if s.pos < len(s.data) && s.data[s.pos] == ',' {
				s.pos++
				continue
			}
			if s.pos < len(s.data) && s.data[s.pos] == '}' {
				s.pos++
				v.end = s.pos
				return v, nil
			}
			return nil, s.fail("expected ',' or '}'")
		}
	case '[':
		v.kind = '['
		s.pos++
		depth := 1
		for s.pos < len(s.data) && depth > 0 {
			switch s.data[s.pos] {
			case '"':
				if _, err := s.str(); err != nil {
					return nil, err
				}
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
			}
			s.pos++
		}
		if depth != 0 {
			return nil, s.fail("unterminated array")
		}
		v.end = s.pos
		return v, nil
	case '"':
		v.kind = '"'
		if _, err := s.str(); err != nil {
			return nil, err
		}
		v.end = s.pos
		return v, nil
	default:
		v.kind = 'v'
		for s.pos < len(s.data) && strings.IndexByte(",}] \t\r\n", s.data[s.pos]) < 0 {
			s.pos++
		}
		if s.pos == v.start {
			return nil, s.fail("unexpected character")
		}
		v.end = s.pos
		return v, nil
	}
}

func parseJSONDoc(data []byte) (*jsonValue, error) {
	s := &jsonScanner{data: data}
	if bytes.HasPrefix(data, []byte("\ufeff")) {
		s.pos = 3
	}
	v, err := s.value()
	if err != nil {
		return nil, err
	}
	if v.kind != '{' {
		return nil, errors.New("package.json must be an object")
	}
	return v, nil
}

func (v *jsonValue) member(key string) *jsonValue {
	for _, m := range v.members {
		if m.key == key {
			return m.value
		}
	}
	return nil
}

// JSONEdit replaces bytes [Start, End) with Text.
type JSONEdit struct {
	Start, End int
	Text       string
}

// ApplyJSONEdits applies non-overlapping edits.
func ApplyJSONEdits(data []byte, edits []JSONEdit) []byte {
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	out := append([]byte{}, data...)
	for _, e := range edits {
		out = append(out[:e.Start], append([]byte(e.Text), out[e.End:]...)...)
	}
	return out
}

func quoteJSON(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// lineIndent returns the leading whitespace of the line containing pos.
func lineIndent(data []byte, pos int) string {
	start := bytes.LastIndexByte(data[:pos], '\n') + 1
	end := start
	for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[start:end])
}

// indentUnit guesses the file's indentation from the root object's first member.
func indentUnit(data []byte, root *jsonValue) string {
	if len(root.members) > 0 {
		if in := lineIndent(data, root.members[0].keyStart); in != "" {
			return in
		}
	}
	return "  "
}

// StringValue returns the span of the string at path (object keys).
func StringValue(data []byte, path ...string) (start, end int, value string, ok bool) {
	root, err := parseJSONDoc(data)
	if err != nil {
		return 0, 0, "", false
	}
	v := root
	for _, k := range path {
		if v.kind != '{' {
			return 0, 0, "", false
		}
		if v = v.member(k); v == nil {
			return 0, 0, "", false
		}
	}
	if v.kind != '"' {
		return 0, 0, "", false
	}
	_ = json.Unmarshal(data[v.start:v.end], &value)
	return v.start, v.end, value, true
}

// SetMember sets the string member key of the object at objPath, creating the
// member and missing objects with the file's indentation.
func SetMember(data []byte, objPath []string, key, value string) (JSONEdit, error) {
	root, err := parseJSONDoc(data)
	if err != nil {
		return JSONEdit{}, err
	}
	unit := indentUnit(data, root)
	obj := root
	i := 0
	for ; i < len(objPath); i++ {
		next := obj.member(objPath[i])
		if next == nil {
			break
		}
		if next.kind != '{' {
			return JSONEdit{}, fmt.Errorf("%s is not an object", strings.Join(objPath[:i+1], "."))
		}
		obj = next
	}
	if i == len(objPath) {
		if v := obj.member(key); v != nil {
			return JSONEdit{Start: v.start, End: v.end, Text: quoteJSON(value)}, nil
		}
	}
	// Build the text for the missing part: objPath[i:] objects plus the member.
	objIndent := lineIndent(data, obj.start)
	if obj == root {
		objIndent = ""
	}
	memberIndent := objIndent + unit
	if len(obj.members) > 0 {
		memberIndent = lineIndent(data, obj.members[0].keyStart)
	}
	text := quoteJSON(value)
	keys := append(append([]string{}, objPath[i:]...), key)
	for j := len(keys) - 1; j >= 1; j-- {
		in := memberIndent + strings.Repeat(unit, j)
		closing := memberIndent + strings.Repeat(unit, j-1)
		text = "{\n" + in + quoteJSON(keys[j]) + ": " + text + "\n" + closing + "}"
	}
	member := quoteJSON(keys[0]) + ": " + text
	if len(obj.members) == 0 {
		return JSONEdit{Start: obj.start + 1, End: obj.end - 1, Text: "\n" + memberIndent + member + "\n" + objIndent}, nil
	}
	last := obj.members[len(obj.members)-1].value
	return JSONEdit{Start: last.end, End: last.end, Text: ",\n" + memberIndent + member}, nil
}
