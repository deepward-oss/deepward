// Package strictyaml decodes YAML nodes into Go structs, rejecting unknown
// keys and reporting every error with its full key path and line number,
// e.g. `deepward.yaml:12: plugins.osv.timeout: invalid duration "fast"`.
package strictyaml

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is a decoding error at a key path.
type Error struct {
	File string
	Line int
	Path string
	Msg  string
}

func (e *Error) Error() string {
	loc := e.File
	if e.Line > 0 {
		if loc != "" {
			loc = fmt.Sprintf("%s:%d", loc, e.Line)
		} else {
			loc = fmt.Sprintf("line %d", e.Line)
		}
	}
	var parts []string
	if loc != "" {
		parts = append(parts, loc)
	}
	if e.Path != "" {
		parts = append(parts, e.Path)
	}
	parts = append(parts, e.Msg)
	return strings.Join(parts, ": ")
}

// Errors collects several decoding errors.
type Errors []*Error

func (es Errors) Error() string {
	msgs := make([]string, len(es))
	for i, e := range es {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

var (
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	yamlUnmarshaler = reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
	nodeType        = reflect.TypeOf(yaml.Node{})
)

// Decode decodes node into out (a non-nil pointer). path is the key path of
// node (empty for the document root); file is used in messages.
func Decode(node *yaml.Node, out any, path, file string) error {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("strictyaml: out must be a non-nil pointer")
	}
	d := &decoder{file: file}
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node == nil {
		return nil
	}
	d.decode(node, v.Elem(), path)
	if len(d.errs) == 0 {
		return nil
	}
	return d.errs
}

type decoder struct {
	file string
	errs Errors
}

func (d *decoder) fail(n *yaml.Node, path, format string, args ...any) {
	d.errs = append(d.errs, &Error{File: d.file, Line: n.Line, Path: path, Msg: fmt.Sprintf(format, args...)})
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func isLeaf(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	return t == nodeType || pt.Implements(textUnmarshaler) || pt.Implements(yamlUnmarshaler) || t.Implements(yamlUnmarshaler)
}

func (d *decoder) decode(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Tag == "!!null" {
		v.Set(reflect.Zero(v.Type()))
		return
	}
	t := v.Type()
	if t == nodeType {
		v.Set(reflect.ValueOf(*n))
		return
	}
	if isLeaf(t) {
		d.leaf(n, v, path)
		return
	}
	switch t.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(t.Elem()))
		}
		d.decode(n, v.Elem(), path)
	case reflect.Struct:
		d.structure(n, v, path)
	case reflect.Map:
		d.mapping(n, v, path)
	case reflect.Slice:
		d.sequence(n, v, path)
	case reflect.Interface:
		var x any
		if err := n.Decode(&x); err != nil {
			d.fail(n, path, "%s", cleanErr(err))
			return
		}
		if x != nil {
			v.Set(reflect.ValueOf(x))
		}
	default:
		d.leaf(n, v, path)
	}
}

func (d *decoder) leaf(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		if _, ok := v.Addr().Interface().(yaml.Unmarshaler); !ok && v.Type() != nodeType {
			if reflect.PointerTo(v.Type()).Implements(textUnmarshaler) || isScalarKind(v.Kind()) {
				d.fail(n, path, "expected a %s value, got a %s", describe(v.Type()), kindName(n.Kind))
				return
			}
		}
	}
	if err := n.Decode(v.Addr().Interface()); err != nil {
		d.fail(n, path, "%s", cleanErr(err))
	}
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func describe(t reflect.Type) string {
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return "scalar"
	}
	return t.Kind().String()
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	}
	return "scalar"
}

func cleanErr(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "yaml: unmarshal errors:\n")
	msg = strings.TrimSpace(msg)
	// yaml.v3 prefixes type errors with "line N: "; the line is already in
	// our message.
	if i := strings.Index(msg, ": "); strings.HasPrefix(msg, "line ") && i > 0 {
		msg = msg[i+2:]
	}
	return msg
}

type field struct {
	index  []int
	inline bool
}

func fieldsOf(t reflect.Type) (map[string]field, []field) {
	named := map[string]field{}
	var inline []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if strings.Contains(opts, "inline") {
			inline = append(inline, field{index: f.Index, inline: true})
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		named[name] = field{index: f.Index}
	}
	return named, inline
}

func (d *decoder) structure(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind != yaml.MappingNode {
		d.fail(n, path, "expected a mapping, got a %s", kindName(n.Kind))
		return
	}
	named, inline := fieldsOf(v.Type())
	// Inline fields contribute their keys (one level).
	type target struct {
		parent []int
		f      field
	}
	keys := map[string]target{}
	for k, f := range named {
		keys[k] = target{f: f}
	}
	for _, in := range inline {
		ft := v.Type().FieldByIndex(in.index).Type
		if ft.Kind() == reflect.Struct {
			sub, _ := fieldsOf(ft)
			for k, f := range sub {
				if _, exists := keys[k]; !exists {
					keys[k] = target{parent: in.index, f: f}
				}
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn, vn := n.Content[i], n.Content[i+1]
		key := kn.Value
		tg, ok := keys[key]
		if !ok {
			known := make([]string, 0, len(keys))
			for k := range keys {
				known = append(known, k)
			}
			sort.Strings(known)
			d.fail(kn, join(path, key), "unknown key %q%s", key, suggest(key, known))
			continue
		}
		fv := v
		if tg.parent != nil {
			fv = v.FieldByIndex(tg.parent)
		}
		d.decode(vn, fv.FieldByIndex(tg.f.index), join(path, key))
	}
}

func (d *decoder) mapping(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind != yaml.MappingNode {
		d.fail(n, path, "expected a mapping, got a %s", kindName(n.Kind))
		return
	}
	if v.IsNil() {
		v.Set(reflect.MakeMap(v.Type()))
	}
	kt, et := v.Type().Key(), v.Type().Elem()
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn, vn := n.Content[i], n.Content[i+1]
		k := reflect.New(kt).Elem()
		if err := kn.Decode(k.Addr().Interface()); err != nil {
			d.fail(kn, join(path, kn.Value), "%s", cleanErr(err))
			continue
		}
		e := reflect.New(et).Elem()
		if existing := v.MapIndex(k); existing.IsValid() {
			e.Set(existing)
		}
		d.decode(vn, e, join(path, kn.Value))
		v.SetMapIndex(k, e)
	}
}

func (d *decoder) sequence(n *yaml.Node, v reflect.Value, path string) {
	if n.Kind != yaml.SequenceNode {
		d.fail(n, path, "expected a list, got a %s", kindName(n.Kind))
		return
	}
	s := reflect.MakeSlice(v.Type(), len(n.Content), len(n.Content))
	for i, item := range n.Content {
		d.decode(item, s.Index(i), fmt.Sprintf("%s[%d]", path, i))
	}
	v.Set(s)
}

// suggest returns a "did you mean" hint for typos.
func suggest(key string, known []string) string {
	best, bestDist := "", 3
	for _, k := range known {
		if dist := levenshtein(strings.ToLower(key), strings.ToLower(k)); dist < bestDist {
			best, bestDist = k, dist
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Parse parses YAML bytes into a document node.
func Parse(data []byte, file string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &Error{File: file, Msg: strings.TrimPrefix(err.Error(), "yaml: ")}
	}
	return &doc, nil
}
