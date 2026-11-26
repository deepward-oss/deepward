// Package purl implements the subset of the package-url specification
// (https://github.com/package-url/purl-spec) that deepward needs, plus
// helpers for Scala cross-version artifact names.
package purl

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Well-known package types.
const (
	TypeMaven  = "maven"
	TypeNuGet  = "nuget"
	TypeNPM    = "npm"
	TypeGolang = "golang"
	TypeCargo  = "cargo"
)

// PURL is a parsed package URL.
type PURL struct {
	Type       string
	Namespace  string
	Name       string
	Version    string
	Qualifiers map[string]string
	Subpath    string
}

// ErrInvalid is returned for strings that are not valid package URLs.
var ErrInvalid = errors.New("invalid purl")

// New builds a PURL and normalizes it.
func New(typ, namespace, name, version string) PURL {
	p := PURL{Type: strings.ToLower(typ), Namespace: namespace, Name: name, Version: version}
	return p.normalize()
}

func (p PURL) normalize() PURL {
	p.Type = strings.ToLower(p.Type)
	if p.Type == TypeNuGet {
		// NuGet IDs are case-insensitive; the canonical purl form is lower case.
		p.Name = strings.ToLower(p.Name)
	}
	return p
}

// Parse parses a package URL string.
func Parse(s string) (PURL, error) {
	rest, ok := strings.CutPrefix(s, "pkg:")
	if !ok {
		return PURL{}, fmt.Errorf("%w %q: missing pkg: scheme", ErrInvalid, s)
	}
	var p PURL
	if i := strings.Index(rest, "#"); i >= 0 {
		p.Subpath = strings.Trim(rest[i+1:], "/")
		rest = rest[:i]
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		q, err := url.ParseQuery(rest[i+1:])
		if err != nil {
			return PURL{}, fmt.Errorf("%w %q: qualifiers: %w", ErrInvalid, s, err)
		}
		p.Qualifiers = map[string]string{}
		for k, v := range q {
			p.Qualifiers[strings.ToLower(k)] = v[0]
		}
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		v, err := url.PathUnescape(rest[i+1:])
		if err != nil {
			return PURL{}, fmt.Errorf("%w %q: version: %w", ErrInvalid, s, err)
		}
		p.Version = v
		rest = rest[:i]
	}
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[len(parts)-1] == "" {
		return PURL{}, fmt.Errorf("%w %q: expected type/name", ErrInvalid, s)
	}
	p.Type = parts[0]
	name, err := url.PathUnescape(parts[len(parts)-1])
	if err != nil {
		return PURL{}, fmt.Errorf("%w %q: name: %w", ErrInvalid, s, err)
	}
	p.Name = name
	if len(parts) > 2 {
		ns := make([]string, 0, len(parts)-2)
		for _, seg := range parts[1 : len(parts)-1] {
			u, err := url.PathUnescape(seg)
			if err != nil {
				return PURL{}, fmt.Errorf("%w %q: namespace: %w", ErrInvalid, s, err)
			}
			ns = append(ns, u)
		}
		p.Namespace = strings.Join(ns, "/")
	}
	return p.normalize(), nil
}

// MustParse is Parse that panics; for tests and constants.
func MustParse(s string) PURL {
	p, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}

func escape(s string) string {
	// PathEscape keeps most characters; '@' and '?' / '#' must be encoded.
	e := url.PathEscape(s)
	e = strings.ReplaceAll(e, "@", "%40")
	return e
}

// String renders the canonical form.
func (p PURL) String() string {
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(p.Type)
	b.WriteByte('/')
	if p.Namespace != "" {
		for _, seg := range strings.Split(p.Namespace, "/") {
			b.WriteString(escape(seg))
			b.WriteByte('/')
		}
	}
	b.WriteString(escape(p.Name))
	if p.Version != "" {
		b.WriteByte('@')
		b.WriteString(escape(p.Version))
	}
	if len(p.Qualifiers) > 0 {
		keys := make([]string, 0, len(p.Qualifiers))
		for k := range p.Qualifiers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('?')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(p.Qualifiers[k]))
		}
	}
	if p.Subpath != "" {
		b.WriteByte('#')
		b.WriteString(p.Subpath)
	}
	return b.String()
}

// WithoutVersion returns the package identity without version, qualifiers or subpath.
func (p PURL) WithoutVersion() PURL {
	return PURL{Type: p.Type, Namespace: p.Namespace, Name: p.Name}
}

// WithVersion returns a copy with the given version.
func (p PURL) WithVersion(v string) PURL {
	p.Version = v
	return p
}

// Coordinates returns the package name as its ecosystem writes it:
// "group:artifact" for Maven, "namespace/name" for types with path-like
// names (npm "@scope/name", Go module paths), otherwise the name.
func (p PURL) Coordinates() string {
	if p.Namespace == "" {
		return p.Name
	}
	if p.Type == TypeMaven {
		return strings.ReplaceAll(p.Namespace, "/", ".") + ":" + p.Name
	}
	return p.Namespace + "/" + p.Name
}

var scalaSuffix = regexp.MustCompile(`^(.+?)((?:_sjs\d+(?:\.\d+)?|_native\d+(?:\.\d+)?)?_(?:2\.1[0-3]|3)(?:_\d+\.\d+)?)$`)

// SplitScala splits a Scala artifact name into its base name and binary
// cross-version suffix, e.g. "cats-core_2.13" -> ("cats-core", "_2.13") and
// "sbt-scalafmt_2.12_1.0" -> ("sbt-scalafmt", "_2.12_1.0"). Names without a
// suffix are returned unchanged with an empty suffix.
func SplitScala(name string) (base, suffix string) {
	m := scalaSuffix.FindStringSubmatch(name)
	if m == nil {
		return name, ""
	}
	return m[1], m[2]
}

// ScalaBase returns the base artifact name (see SplitScala).
func (p PURL) ScalaBase() string { b, _ := SplitScala(p.Name); return b }

// ScalaSuffix returns the binary cross-version suffix (see SplitScala).
func (p PURL) ScalaSuffix() string { _, s := SplitScala(p.Name); return s }
