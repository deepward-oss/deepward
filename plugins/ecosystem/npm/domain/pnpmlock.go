package domain

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type pnpmDeps struct {
	Dependencies         map[string]yaml.Node `yaml:"dependencies"`
	DevDependencies      map[string]yaml.Node `yaml:"devDependencies"`
	OptionalDependencies map[string]yaml.Node `yaml:"optionalDependencies"`
}

type pnpmPackage struct {
	Name                 string            `yaml:"name"`
	Version              string            `yaml:"version"`
	Dependencies         map[string]string `yaml:"dependencies"`
	OptionalDependencies map[string]string `yaml:"optionalDependencies"`
}

type pnpmLock struct {
	LockfileVersion string                 `yaml:"lockfileVersion"`
	Importers       map[string]pnpmDeps    `yaml:"importers"`
	Packages        map[string]pnpmPackage `yaml:"packages"`
	Snapshots       map[string]pnpmPackage `yaml:"snapshots"`
	pnpmDeps        `yaml:",inline"`
}

// ParsePNPMLock parses pnpm-lock.yaml (lockfile versions 5.x, 6 and 9).
func ParsePNPMLock(data []byte) (*Tree, error) {
	var lock pnpmLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("pnpm-lock.yaml: %w", err)
	}
	ver, err := strconv.ParseFloat(strings.TrimSpace(lock.LockfileVersion), 64)
	if err != nil {
		return nil, fmt.Errorf("pnpm-lock.yaml: invalid lockfileVersion %q", lock.LockfileVersion)
	}
	major := int(ver)
	switch major {
	case 5, 6, 9:
	default:
		return nil, fmt.Errorf("pnpm-lock.yaml: unsupported lockfileVersion %s", lock.LockfileVersion)
	}
	edges := lock.Packages
	if major == 9 {
		edges = lock.Snapshots
	}
	t := &Tree{Packages: map[string]*Package{}, Format: "pnpm-lock.yaml v" + lock.LockfileVersion}
	keyOf := func(name, ref string) (string, bool) {
		if strings.HasPrefix(ref, "link:") || strings.HasPrefix(ref, "file:") || ref == "" {
			return "", false
		}
		var key string
		switch {
		case major == 9 && (ref[0] >= '0' && ref[0] <= '9'):
			key = name + "@" + ref
		case major == 9:
			key = ref
		case strings.HasPrefix(ref, "/"):
			key = ref
		case major == 6:
			key = "/" + name + "@" + ref
		default:
			key = "/" + name + "/" + ref
		}
		_, ok := edges[key]
		return key, ok
	}
	for key, e := range edges {
		name, version := pnpmNameVersion(key, major)
		if meta, ok := lock.Packages[pnpmBaseKey(key, major)]; ok && major == 9 {
			if meta.Name != "" {
				name = meta.Name
			}
			if meta.Version != "" {
				version = meta.Version
			}
		}
		if e.Name != "" {
			name = e.Name
		}
		if e.Version != "" {
			version = e.Version
		}
		p := &Package{Name: name, Version: version}
		for _, section := range []map[string]string{e.Dependencies, e.OptionalDependencies} {
			for _, dn := range SortedKeys(section) {
				if k, ok := keyOf(dn, section[dn]); ok {
					p.Deps = append(p.Deps, k)
				}
			}
		}
		t.Packages[key] = p
	}
	importers := lock.Importers
	if len(importers) == 0 {
		importers = map[string]pnpmDeps{".": lock.pnpmDeps}
	}
	for _, dir := range SortedKeys(importers) {
		imp := Importer{Dir: dir}
		d := importers[dir]
		seen := map[string]bool{}
		for _, s := range []struct {
			deps map[string]yaml.Node
			dev  bool
		}{{d.Dependencies, false}, {d.OptionalDependencies, false}, {d.DevDependencies, true}} {
			for _, name := range SortedKeys(s.deps) {
				n := s.deps[name]
				if seen[name] {
					continue
				}
				if k, ok := keyOf(name, importerRef(&n)); ok {
					seen[name] = true
					imp.Deps = append(imp.Deps, DirectDep{Name: name, Key: k, Dev: s.dev})
				}
			}
		}
		t.Importers = append(t.Importers, imp)
	}
	return t, nil
}

// importerRef reads an importer dependency: a version string (v5) or a
// {specifier, version} mapping (v6, v9).
func importerRef(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	var v struct {
		Version string `yaml:"version"`
	}
	_ = n.Decode(&v)
	return v.Version
}

// pnpmBaseKey strips the peer suffix of a v9 snapshot key.
func pnpmBaseKey(key string, major int) string {
	if major >= 6 {
		if i := strings.Index(key, "("); i > 0 {
			return key[:i]
		}
	}
	return key
}

// pnpmNameVersion extracts the package name and version from a key:
// "name@1.0.0(peer@1)" (v9), "/name@1.0.0(peer@1)" (v6) or
// "/name/1.0.0_peer@1" (v5).
func pnpmNameVersion(key string, major int) (string, string) {
	k := strings.TrimPrefix(pnpmBaseKey(key, major), "/")
	if major >= 6 {
		name, version := SplitDescriptor(k)
		return name, version
	}
	segs := strings.Split(k, "/")
	n := 1
	if strings.HasPrefix(segs[0], "@") {
		n = 2
	}
	if len(segs) <= n {
		return k, ""
	}
	version, _, _ := strings.Cut(segs[n], "_")
	return strings.Join(segs[:n], "/"), version
}
