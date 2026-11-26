package domain

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsBerryLock reports whether yarn.lock was written by Yarn 2 or later.
func IsBerryLock(data []byte) bool {
	return bytes.Contains(data, []byte("\n__metadata:")) || bytes.HasPrefix(data, []byte("__metadata:"))
}

// SplitDescriptor splits "name@range" (the name may be scoped).
func SplitDescriptor(d string) (name, rng string) {
	start := 0
	if strings.HasPrefix(d, "@") {
		start = 1
	}
	i := strings.Index(d[start:], "@")
	if i < 0 {
		return d, ""
	}
	return d[:start+i], d[start+i+1:]
}

// ParseYarnLock parses yarn.lock (v1 or Berry). manifests maps package
// directories relative to the lockfile ("." for the root) to their
// package.json; they supply the direct dependencies.
func ParseYarnLock(data []byte, manifests map[string]*Manifest) (*Tree, error) {
	if IsBerryLock(data) {
		return parseBerry(data, manifests)
	}
	return parseYarnV1(data, manifests)
}

type yarnBlock struct {
	descriptors []string
	version     string
	deps        map[string]string
}

// yarnFields splits a yarn v1 line into whitespace-separated fields,
// honouring double quotes.
func yarnFields(line string) []string {
	var out []string
	var cur strings.Builder
	inQuote, has := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			has = true
		case !inQuote && (c == ' ' || c == '\t'):
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func parseYarnV1(data []byte, manifests map[string]*Manifest) (*Tree, error) {
	var blocks []*yarnBlock
	var cur *yarnBlock
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(text) - len(strings.TrimLeft(text, " "))
		switch {
		case indent == 0:
			if !strings.HasSuffix(trimmed, ":") {
				return nil, fmt.Errorf("yarn.lock:%d: expected a package entry", line)
			}
			cur = &yarnBlock{deps: map[string]string{}}
			for _, d := range strings.Split(strings.TrimSuffix(trimmed, ":"), ",") {
				d = strings.TrimSpace(d)
				if u, err := strconv.Unquote(d); err == nil {
					d = u
				}
				cur.descriptors = append(cur.descriptors, d)
			}
			blocks = append(blocks, cur)
			section = ""
		case cur == nil:
			return nil, fmt.Errorf("yarn.lock:%d: field outside an entry", line)
		case indent == 2:
			f := yarnFields(trimmed)
			section = ""
			switch {
			case len(f) == 1 && strings.HasSuffix(f[0], ":"):
				section = strings.TrimSuffix(f[0], ":")
			case len(f) >= 2 && f[0] == "version":
				cur.version = f[1]
			}
		default:
			if section != "dependencies" && section != "optionalDependencies" {
				continue
			}
			f := yarnFields(trimmed)
			if len(f) >= 2 {
				cur.deps[strings.TrimSuffix(f[0], ":")] = f[1]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("yarn.lock: %w", err)
	}
	t := &Tree{Packages: map[string]*Package{}, Format: "yarn.lock v1"}
	index := map[string]string{}
	for _, b := range blocks {
		name, rng := SplitDescriptor(b.descriptors[0])
		if real, ok := aliasTarget(rng); ok {
			name = real
		}
		key := name + "@" + b.version
		local := strings.HasPrefix(rng, "file:") || strings.HasPrefix(rng, "link:")
		if local {
			key = b.descriptors[0]
		}
		if _, ok := t.Packages[key]; !ok {
			t.Packages[key] = &Package{Name: name, Version: b.version, Local: local}
		}
		for _, d := range b.descriptors {
			index[d] = key
		}
	}
	for _, b := range blocks {
		key := index[b.descriptors[0]]
		p := t.Packages[key]
		for _, name := range SortedKeys(b.deps) {
			if k, ok := index[name+"@"+b.deps[name]]; ok && k != key {
				p.Deps = append(p.Deps, k)
			}
		}
	}
	t.Importers = importersFromManifests(manifests, func(name, rng string) (string, bool) {
		k, ok := index[name+"@"+rng]
		return k, ok
	})
	return t, nil
}

// aliasTarget returns the real package of an alias range "npm:real@^1".
func aliasTarget(rng string) (string, bool) {
	rest, ok := strings.CutPrefix(rng, "npm:")
	if !ok {
		return "", false
	}
	name, r := SplitDescriptor(rest)
	if r == "" || name == "" {
		return "", false
	}
	return name, true
}

func importersFromManifests(manifests map[string]*Manifest, lookup func(name, rng string) (string, bool)) []Importer {
	local := map[string]bool{}
	for _, m := range manifests {
		if m.Name != "" {
			local[m.Name] = true
		}
	}
	var out []Importer
	for _, dir := range SortedKeys(manifests) {
		m := manifests[dir]
		imp := Importer{Dir: dir}
		direct := m.DirectDeps()
		for _, name := range SortedKeys(direct) {
			if local[name] {
				continue
			}
			rng := ""
			for _, k := range []DepKind{KindProd, KindOptional, KindDev} {
				if r, ok := m.Deps[k][name]; ok {
					rng = r
					break
				}
			}
			if key, ok := lookup(name, rng); ok {
				imp.Deps = append(imp.Deps, DirectDep{Name: name, Key: key, Dev: direct[name]})
			}
		}
		out = append(out, imp)
	}
	return out
}

type berryEntry struct {
	Version      string            `yaml:"version"`
	Resolution   string            `yaml:"resolution"`
	Dependencies map[string]string `yaml:"dependencies"`
	LinkType     string            `yaml:"linkType"`
}

func parseBerry(data []byte, manifests map[string]*Manifest) (*Tree, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("yarn.lock: %w", err)
	}
	var meta struct {
		Version string `yaml:"version"`
	}
	if n, ok := raw["__metadata"]; ok {
		_ = n.Decode(&meta)
	}
	t := &Tree{Packages: map[string]*Package{}, Format: "yarn.lock (Berry, v" + meta.Version + ")"}
	index := map[string]string{}
	entries := map[string]berryEntry{}
	for _, keys := range SortedKeys(raw) {
		if keys == "__metadata" {
			continue
		}
		var e berryEntry
		n := raw[keys]
		if err := n.Decode(&e); err != nil {
			return nil, fmt.Errorf("yarn.lock: %s: %w", keys, err)
		}
		key := e.Resolution
		if key == "" {
			key = keys
		}
		name, rng := SplitDescriptor(key)
		local := strings.HasPrefix(rng, "workspace:") || strings.HasPrefix(rng, "link:") || strings.HasPrefix(rng, "portal:") || strings.HasPrefix(rng, "file:")
		t.Packages[key] = &Package{Name: name, Version: e.Version, Local: local}
		entries[key] = e
		for _, d := range strings.Split(keys, ",") {
			index[strings.TrimSpace(d)] = key
		}
	}
	lookup := func(name, rng string) (string, bool) {
		if k, ok := index[name+"@"+rng]; ok {
			return k, true
		}
		k, ok := index[name+"@npm:"+rng]
		return k, ok
	}
	byDir := map[string]*Manifest{}
	for dir, m := range manifests {
		byDir[dir] = m
	}
	for _, key := range SortedKeys(entries) {
		e := entries[key]
		p := t.Packages[key]
		for _, name := range SortedKeys(e.Dependencies) {
			if k, ok := lookup(name, e.Dependencies[name]); ok && k != key {
				p.Deps = append(p.Deps, k)
			}
		}
		_, rng := SplitDescriptor(key)
		dir, isWorkspace := strings.CutPrefix(rng, "workspace:")
		if !isWorkspace {
			continue
		}
		imp := Importer{Dir: dir}
		var direct map[string]bool
		if m := byDir[dir]; m != nil {
			direct = m.DirectDeps()
		}
		for _, name := range SortedKeys(e.Dependencies) {
			k, ok := lookup(name, e.Dependencies[name])
			if !ok || t.Packages[k].Local {
				continue
			}
			imp.Deps = append(imp.Deps, DirectDep{Name: name, Key: k, Dev: direct[name]})
		}
		t.Importers = append(t.Importers, imp)
	}
	sortImporters(t)
	return t, nil
}
