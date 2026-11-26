package domain

import (
	"regexp"
	"strings"
)

// Span is the byte range of a requirement string's content (without quotes).
type Span struct {
	Start, End int
	Value      string
}

var depSection = regexp.MustCompile(`^(?:workspace\.|target\.(?:'[^']*'|"[^"]*"|[^.]+)\.)?(?:dev-|dev_|build-|build_)?dependencies$`)

// headerName normalizes a table header: brackets and spaces around dots are
// removed, quoted keys keep their content.
func headerName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[[") {
		// Arrays of tables ([[bin]], [[test]]) are never dependency tables.
		return "[[]]", true
	}
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	end := strings.LastIndex(t, "]")
	if end < 0 {
		return "", false
	}
	parts := splitKey(t[1:end])
	for i, p := range parts {
		if strings.ContainsAny(p, ".") {
			parts[i] = `"` + p + `"`
		}
	}
	return strings.Join(parts, "."), true
}

// splitKey splits a dotted TOML key into unquoted parts.
func splitKey(k string) []string {
	var parts []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
		case c == '.':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		case c != ' ' && c != '\t':
			cur.WriteByte(c)
		}
	}
	return append(parts, strings.TrimSpace(cur.String()))
}

// stringAt returns the span of a basic or literal string starting at the
// first quote at or after i on the line.
func stringAt(line string, i, offset int) (Span, bool) {
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || (line[i] != '"' && line[i] != '\'') {
		return Span{}, false
	}
	q := line[i]
	end := strings.IndexByte(line[i+1:], q)
	if end < 0 {
		return Span{}, false
	}
	return Span{Start: offset + i + 1, End: offset + i + 1 + end, Value: line[i+1 : i+1+end]}, true
}

var inlineVersion = regexp.MustCompile(`(?:^|[{,\s])version\s*=\s*`)

// FindRequirements returns the requirement strings declared for the table
// key in every dependency section of a Cargo.toml (including
// [workspace.dependencies] and target-specific tables): `key = "1.0"`,
// `key = { version = "1.0" }`, `key.version = "1.0"` and
// `[dependencies.key]` tables with `version = "1.0"`.
func FindRequirements(data []byte, key string) []Span {
	var out []Span
	section := ""    // dependency table header, or ""
	inTable := false // inside [<deps>.key]
	offset := 0
	for _, line := range strings.SplitAfter(string(data), "\n") {
		lineStart := offset
		offset += len(line)
		if h, ok := headerName(line); ok {
			section, inTable = "", false
			if depSection.MatchString(h) {
				section = h
			} else if i := strings.LastIndex(h, "."); i > 0 && depSection.MatchString(h[:i]) && strings.Trim(h[i+1:], `"`) == key {
				inTable = true
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k := splitKey(line[:eq])
		switch {
		case inTable && len(k) == 1 && k[0] == "version":
			if s, ok := stringAt(line, eq+1, lineStart); ok {
				out = append(out, s)
			}
		case section != "" && len(k) == 2 && k[0] == key && k[1] == "version":
			if s, ok := stringAt(line, eq+1, lineStart); ok {
				out = append(out, s)
			}
		case section != "" && len(k) == 1 && k[0] == key:
			rest := line[eq+1:]
			if s, ok := stringAt(line, eq+1, lineStart); ok {
				out = append(out, s)
				continue
			}
			if b := strings.Index(rest, "{"); b >= 0 {
				if loc := inlineVersion.FindStringIndex(rest[b:]); loc != nil {
					if s, ok := stringAt(line, eq+1+b+loc[1], lineStart); ok {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}
