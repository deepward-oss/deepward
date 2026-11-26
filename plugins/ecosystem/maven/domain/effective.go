package domain

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// FetchPOM returns the POM of a published artifact version.
type FetchPOM func(ctx context.Context, group, artifact, version string) ([]byte, error)

// maxDepth bounds parent chains and BOM imports (and breaks cycles).
const maxDepth = 12

// Resolver computes the runtime dependencies a published artifact declares,
// the way Maven builds its effective model: properties and
// dependencyManagement are inherited from the parent chain, BOMs imported
// with <scope>import</scope> fill in managed versions, and ${...} references
// are interpolated. It does not resolve version conflicts with other
// artifacts. Results are memoized; a Resolver is safe for concurrent use.
type Resolver struct {
	Fetch FetchPOM

	mu      sync.Mutex
	models  map[string]modelResult
	managed map[string]managedResult
}

type modelResult struct {
	m   *model
	err error
}

type managedResult struct {
	m   map[string]managedDep
	err error
}

// model is a POM merged with its parent chain, values not yet interpolated.
type model struct {
	props map[string]string
	// managed lists dependencyManagement entries, own entries before
	// inherited ones (the first match wins).
	managed []Dependency
	deps    []Dependency
}

type managedDep struct{ version, scope string }

// Dependencies returns the runtime dependencies of group:artifact:version as
// "group:artifact" → version. Dependencies in test, provided, system or
// import scope and optional ones are left out. A version that cannot be
// determined (an unresolved property or a version range) is "".
func (r *Resolver) Dependencies(ctx context.Context, group, artifact, version string) (map[string]string, error) {
	m, err := r.model(ctx, group, artifact, version, 0)
	if err != nil {
		return nil, err
	}
	managed, err := r.effectiveManaged(ctx, group, artifact, version, 0)
	if err != nil {
		return nil, err
	}
	interp := m.interpolator()
	out := map[string]string{}
	for _, d := range m.deps {
		coord := interp(d.GroupID.Text) + ":" + interp(d.ArtifactID.Text)
		md := managed[coord]
		scope := firstNonEmpty(interp(d.Scope), md.scope, "compile")
		if scope == "test" || scope == "provided" || scope == "system" || scope == "import" || interp(d.Optional) == "true" {
			continue
		}
		v := interp(d.Version.Text)
		if v == "" {
			v = md.version
		}
		out[coord] = concrete(v)
	}
	return out, nil
}

// concrete returns v if it is a plain version, else "".
func concrete(v string) string {
	if v == "" || strings.Contains(v, "${") || strings.ContainsAny(v[:1], "[(") {
		return ""
	}
	return v
}

func key(group, artifact, version string) string { return group + ":" + artifact + ":" + version }

func (r *Resolver) model(ctx context.Context, group, artifact, version string, depth int) (*model, error) {
	k := key(group, artifact, version)
	r.mu.Lock()
	if res, ok := r.models[k]; ok {
		r.mu.Unlock()
		return res.m, res.err
	}
	r.mu.Unlock()
	m, err := r.loadModel(ctx, group, artifact, version, depth)
	if ctx.Err() != nil {
		return nil, err // do not memoize cancellations
	}
	r.mu.Lock()
	if r.models == nil {
		r.models = map[string]modelResult{}
	}
	r.models[k] = modelResult{m, err}
	r.mu.Unlock()
	return m, err
}

func (r *Resolver) loadModel(ctx context.Context, group, artifact, version string, depth int) (*model, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%s: parent chain is too deep", key(group, artifact, version))
	}
	data, err := r.Fetch(ctx, group, artifact, version)
	if err != nil {
		return nil, err
	}
	pom, err := ParsePOM(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key(group, artifact, version), err)
	}
	m := &model{props: map[string]string{}}
	if p := pom.Parent; p != nil && p.GroupID.Text != "" && p.ArtifactID.Text != "" && p.Version.Text != "" {
		parent, err := r.model(ctx, p.GroupID.Text, p.ArtifactID.Text, p.Version.Text, depth+1)
		if err != nil {
			return nil, fmt.Errorf("parent of %s: %w", key(group, artifact, version), err)
		}
		for k, v := range parent.props {
			m.props[k] = v
		}
		m.managed = append(m.managed, parent.managed...)
		m.deps = append(m.deps, parent.deps...)
		m.props["project.parent.groupId"] = p.GroupID.Text
		m.props["project.parent.version"] = p.Version.Text
		m.props["parent.version"] = p.Version.Text
	}
	for k, v := range pom.Properties {
		m.props[k] = v.Text
	}
	g, v := firstNonEmpty(pom.EffectiveGroupID(), group), firstNonEmpty(pom.EffectiveVersion(), version)
	for _, name := range []string{"project.groupId", "pom.groupId", "groupId"} {
		m.props[name] = g
	}
	for _, name := range []string{"project.artifactId", "pom.artifactId", "artifactId"} {
		m.props[name] = firstNonEmpty(pom.ArtifactID.Text, artifact)
	}
	for _, name := range []string{"project.version", "pom.version", "version"} {
		m.props[name] = v
	}
	// Own managed entries take precedence over inherited ones.
	m.managed = append(append([]Dependency{}, pom.Managed...), m.managed...)
	// Own dependencies override inherited ones with the same coordinates.
	own := map[string]bool{}
	for _, d := range pom.Dependencies {
		own[d.Coordinates()] = true
	}
	deps := make([]Dependency, 0, len(m.deps)+len(pom.Dependencies))
	for _, d := range m.deps {
		if !own[d.Coordinates()] {
			deps = append(deps, d)
		}
	}
	deps = append(deps, pom.Dependencies...)
	m.deps = deps
	return m, nil
}

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// interpolator resolves ${name} references against the model's properties
// (nested references included); unknown references are left as they are.
func (m *model) interpolator() func(string) string {
	return func(s string) string {
		for i := 0; i < maxDepth && strings.Contains(s, "${"); i++ {
			next := refPattern.ReplaceAllStringFunc(s, func(ref string) string {
				if v, ok := m.props[ref[2:len(ref)-1]]; ok {
					return v
				}
				return ref
			})
			if next == s {
				break
			}
			s = next
		}
		return strings.TrimSpace(s)
	}
}

// effectiveManaged returns the dependencyManagement of an artifact:
// explicit entries (own, then inherited) win over imported BOMs, and earlier
// imports win over later ones.
func (r *Resolver) effectiveManaged(ctx context.Context, group, artifact, version string, depth int) (map[string]managedDep, error) {
	k := key(group, artifact, version)
	r.mu.Lock()
	if res, ok := r.managed[k]; ok {
		r.mu.Unlock()
		return res.m, res.err
	}
	r.mu.Unlock()
	out, err := r.loadManaged(ctx, group, artifact, version, depth)
	if ctx.Err() != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.managed == nil {
		r.managed = map[string]managedResult{}
	}
	r.managed[k] = managedResult{out, err}
	r.mu.Unlock()
	return out, err
}

func (r *Resolver) loadManaged(ctx context.Context, group, artifact, version string, depth int) (map[string]managedDep, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%s: BOM imports are too deep", key(group, artifact, version))
	}
	m, err := r.model(ctx, group, artifact, version, 0)
	if err != nil {
		return nil, err
	}
	interp := m.interpolator()
	out := map[string]managedDep{}
	var imports [][3]string
	for _, d := range m.managed {
		coord := interp(d.GroupID.Text) + ":" + interp(d.ArtifactID.Text)
		scope := interp(d.Scope)
		if scope == "import" && firstNonEmpty(interp(d.Type), "jar") == "pom" {
			imports = append(imports, [3]string{interp(d.GroupID.Text), interp(d.ArtifactID.Text), interp(d.Version.Text)})
			continue
		}
		if _, ok := out[coord]; !ok {
			out[coord] = managedDep{version: concrete(interp(d.Version.Text)), scope: scope}
		}
	}
	for _, imp := range imports {
		if concrete(imp[2]) == "" {
			continue
		}
		bom, err := r.effectiveManaged(ctx, imp[0], imp[1], imp[2], depth+1)
		if err != nil {
			// A BOM that cannot be read only leaves its versions unknown.
			continue
		}
		for coord, md := range bom {
			if _, ok := out[coord]; !ok {
				out[coord] = md
			}
		}
	}
	return out, nil
}
