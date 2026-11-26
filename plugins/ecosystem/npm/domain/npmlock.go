package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

type npmEntry struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Link                 bool              `json:"link"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

type npmLock struct {
	LockfileVersion int                  `json:"lockfileVersion"`
	Packages        map[string]*npmEntry `json:"packages"`
	Dependencies    json.RawMessage      `json:"dependencies"`
}

// ParseNPMLock parses package-lock.json or npm-shrinkwrap.json (lockfile
// versions 1–3). root is the package.json next to the lockfile; it supplies
// the direct dependencies of version 1 lockfiles.
func ParseNPMLock(data []byte, root *Manifest) (*Tree, error) {
	var lock npmLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("package-lock.json: %w", err)
	}
	switch {
	case lock.LockfileVersion >= 2 && lock.LockfileVersion <= 3 && lock.Packages != nil:
		return npmPackagesTree(lock.Packages, fmt.Sprintf("package-lock.json v%d", lock.LockfileVersion)), nil
	case lock.LockfileVersion == 1:
		pkgs := map[string]*npmEntry{}
		if err := flattenV1(lock.Dependencies, "", pkgs); err != nil {
			return nil, err
		}
		top := &npmEntry{}
		if root != nil {
			top.Dependencies = root.Deps[KindProd]
			top.OptionalDependencies = root.Deps[KindOptional]
			top.DevDependencies = root.Deps[KindDev]
		}
		pkgs[""] = top
		return npmPackagesTree(pkgs, "package-lock.json v1"), nil
	}
	return nil, fmt.Errorf("package-lock.json: unsupported lockfileVersion %d", lock.LockfileVersion)
}

// flattenV1 turns the nested v1 "dependencies" tree into v2-style
// node_modules locations.
func flattenV1(raw json.RawMessage, parent string, out map[string]*npmEntry) error {
	if len(raw) == 0 {
		return nil
	}
	var deps map[string]json.RawMessage
	if err := json.Unmarshal(raw, &deps); err != nil {
		return fmt.Errorf("package-lock.json: dependencies: %w", err)
	}
	for name, body := range deps {
		// In v1 "dependencies" of an entry are nested installs; "requires"
		// are its dependency ranges.
		var v1 struct {
			Version      string            `json:"version"`
			Requires     map[string]string `json:"requires"`
			Dependencies json.RawMessage   `json:"dependencies"`
		}
		if err := json.Unmarshal(body, &v1); err != nil {
			return fmt.Errorf("package-lock.json: %s: %w", name, err)
		}
		key := "node_modules/" + name
		if parent != "" {
			key = parent + "/node_modules/" + name
		}
		e := npmEntry{Name: name, Version: v1.Version, Dependencies: v1.Requires}
		if real, ver, ok := aliasVersion(e.Version); ok {
			e.Name, e.Version = real, ver
		}
		out[key] = &e
		nested := v1
		if err := flattenV1(nested.Dependencies, key, out); err != nil {
			return err
		}
	}
	return nil
}

// aliasVersion splits a v1 alias version "npm:real@1.2.3".
func aliasVersion(v string) (name, version string, ok bool) {
	rest, ok := strings.CutPrefix(v, "npm:")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, "@")
	if i <= 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

func nameFromLocation(loc string) string {
	i := strings.LastIndex(loc, "node_modules/")
	if i < 0 {
		return ""
	}
	return loc[i+len("node_modules/"):]
}

// isLocalLocation reports whether a lockfile location is the root or a
// folder outside node_modules (a workspace member or linked folder).
func isLocalLocation(loc string) bool {
	return loc == "" || !strings.Contains(loc, "node_modules/")
}

// resolveNPM finds the location that satisfies a dependency of the package at
// loc, following Node's module resolution (nearest node_modules upwards) and
// links.
func resolveNPM(pkgs map[string]*npmEntry, loc, name string) (string, bool) {
	for {
		cand := "node_modules/" + name
		if loc != "" {
			cand = loc + "/node_modules/" + name
		}
		if e, ok := pkgs[cand]; ok {
			if e.Link {
				if _, ok := pkgs[e.Resolved]; ok {
					return e.Resolved, true
				}
				return "", false
			}
			return cand, true
		}
		if loc == "" {
			return "", false
		}
		if i := strings.LastIndex(loc, "/node_modules/"); i >= 0 {
			loc = loc[:i]
		} else {
			loc = ""
		}
	}
}

func npmPackagesTree(pkgs map[string]*npmEntry, format string) *Tree {
	t := &Tree{Packages: map[string]*Package{}, Format: format}
	for loc, e := range pkgs {
		if e.Link {
			continue
		}
		p := &Package{Name: e.Name, Version: e.Version, Local: isLocalLocation(loc)}
		if p.Name == "" {
			p.Name = nameFromLocation(loc)
		}
		if strings.HasPrefix(p.Version, "file:") {
			p.Local = true
		}
		for _, section := range []map[string]string{e.Dependencies, e.OptionalDependencies, e.PeerDependencies} {
			for _, name := range SortedKeys(section) {
				if key, ok := resolveNPM(pkgs, loc, name); ok {
					p.Deps = append(p.Deps, key)
				}
			}
		}
		t.Packages[loc] = p
		if !isLocalLocation(loc) {
			continue
		}
		dir := loc
		if dir == "" {
			dir = "."
		}
		imp := Importer{Dir: dir}
		seen := map[string]bool{}
		for _, s := range []struct {
			deps map[string]string
			dev  bool
		}{{e.Dependencies, false}, {e.OptionalDependencies, false}, {e.PeerDependencies, false}, {e.DevDependencies, true}} {
			for _, name := range SortedKeys(s.deps) {
				if seen[name] {
					continue
				}
				if key, ok := resolveNPM(pkgs, loc, name); ok {
					seen[name] = true
					imp.Deps = append(imp.Deps, DirectDep{Name: name, Key: key, Dev: s.dev})
				}
			}
		}
		t.Importers = append(t.Importers, imp)
	}
	sortImporters(t)
	return t
}

func sortImporters(t *Tree) {
	for i := 1; i < len(t.Importers); i++ {
		for j := i; j > 0 && t.Importers[j].Dir < t.Importers[j-1].Dir; j-- {
			t.Importers[j], t.Importers[j-1] = t.Importers[j-1], t.Importers[j]
		}
	}
}
