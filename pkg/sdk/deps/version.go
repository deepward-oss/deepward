// Package deps contains the SDK contracts and value types shared by the
// dependency-analysis plugin set: package ecosystems and versions, the
// dependency-graph fact, version registries and advisory sources. The core
// SDK (package sdk) does not depend on it.
package deps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/osv-scalibr/semantic"

	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// Ecosystem is a package ecosystem, named as in OSV.
type Ecosystem string

// Supported package ecosystems.
const (
	Maven    Ecosystem = "Maven"
	NuGet    Ecosystem = "NuGet"
	NPM      Ecosystem = "npm"
	Go       Ecosystem = "Go"
	CratesIO Ecosystem = "crates.io"
)

// Ecosystems lists the supported ecosystems.
var Ecosystems = []Ecosystem{Maven, NuGet, NPM, Go, CratesIO}

// SemVer reports whether the ecosystem versions by Semantic Versioning.
func (e Ecosystem) SemVer() bool { return e == NuGet || e == NPM || e == Go || e == CratesIO }

// PURLType returns the package-url type for the ecosystem.
func (e Ecosystem) PURLType() string {
	switch e {
	case Maven:
		return purl.TypeMaven
	case NuGet:
		return purl.TypeNuGet
	case NPM:
		return purl.TypeNPM
	case Go:
		return purl.TypeGolang
	case CratesIO:
		return purl.TypeCargo
	}
	return strings.ToLower(string(e))
}

// EcosystemOf returns the ecosystem of a package URL type.
func EcosystemOf(purlType string) (Ecosystem, error) {
	switch strings.ToLower(purlType) {
	case purl.TypeMaven:
		return Maven, nil
	case purl.TypeNuGet:
		return NuGet, nil
	case purl.TypeNPM:
		return NPM, nil
	case purl.TypeGolang:
		return Go, nil
	case purl.TypeCargo:
		return CratesIO, nil
	}
	return "", fmt.Errorf("unsupported package type %q", purlType)
}

// Version is a parsed version that compares by its ecosystem's rules
// (Maven ComparableVersion, SemVer 2.0 for the others), using the same comparators
// OSV uses for its affected ranges.
type Version struct {
	eco Ecosystem
	raw string
	sem semantic.Version
}

// ParseVersion parses a version string of the given ecosystem.
func ParseVersion(eco Ecosystem, s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("empty %s version", eco)
	}
	switch eco {
	case Maven, NuGet, NPM, CratesIO:
	case Go:
		// Go versions are canonical with a leading "v"; OSV ranges omit it.
		if !strings.HasPrefix(s, "v") {
			s = "v" + s
		}
	default:
		return Version{}, fmt.Errorf("unsupported ecosystem %q", eco)
	}
	v, err := semantic.Parse(s, string(eco))
	if err != nil {
		return Version{}, fmt.Errorf("parse %s version %q: %w", eco, s, err)
	}
	return Version{eco: eco, raw: s, sem: v}, nil
}

// MustParseVersion is ParseVersion that panics; for tests.
func MustParseVersion(eco Ecosystem, s string) Version {
	v, err := ParseVersion(eco, s)
	if err != nil {
		panic(err)
	}
	return v
}

// String returns the original version text.
func (v Version) String() string { return v.raw }

// Ecosystem returns the version's ecosystem.
func (v Version) Ecosystem() Ecosystem { return v.eco }

// IsZero reports whether v is the zero Version.
func (v Version) IsZero() bool { return v.sem == nil }

// Compare returns -1, 0 or +1. Versions of different ecosystems compare by
// their raw text, which keeps sorting total but is otherwise meaningless.
func (v Version) Compare(o Version) int {
	if v.sem == nil || o.sem == nil || v.eco != o.eco {
		return strings.Compare(v.raw, o.raw)
	}
	c, err := v.sem.Compare(o.sem)
	if err != nil {
		return strings.Compare(v.raw, o.raw)
	}
	return c
}

// Less reports v < o.
func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

var (
	mavenPrerelease = regexp.MustCompile(`(?i)(^|[.\-_])(alpha|a|beta|b|milestone|m|rc|cr|snapshot|preview|pre|ea|dev|incubating)(\d*)([.\-_]|$)|(?i)-SNAPSHOT$`)
	numericPrefix   = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?`)
)

// IsPrerelease reports whether the version is a prerelease: a SemVer
// prerelease tag (including Go pseudo-versions), or a Maven qualifier such as
// -M1, -RC2, -beta or -SNAPSHOT.
func (v Version) IsPrerelease() bool {
	switch {
	case v.eco.SemVer():
		core, _, _ := strings.Cut(v.raw, "+")
		return strings.Contains(core, "-")
	default:
		// Only look at the part after the numeric prefix so that names like
		// "1.0.0.Final" or "2.0-m" edge cases are handled by the qualifier list.
		rest := numericPrefix.ReplaceAllString(v.raw, "")
		return rest != "" && mavenPrerelease.MatchString(rest)
	}
}

// Segments returns the leading numeric major, minor and patch components
// (missing components are 0) and whether a numeric prefix exists at all.
func (v Version) Segments() (major, minor, patch int, ok bool) {
	m := numericPrefix.FindStringSubmatch(v.raw)
	if m == nil {
		return 0, 0, 0, false
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	return atoi(m[1]), atoi(m[2]), atoi(m[3]), true
}

// UpdateType classifies an update from one version to a newer one.
type UpdateType string

// Update types.
const (
	UpdatePatch UpdateType = "patch"
	UpdateMinor UpdateType = "minor"
	UpdateMajor UpdateType = "major"
	UpdateNone  UpdateType = ""
)

// Rank orders update types: major > minor > patch > none.
func (u UpdateType) Rank() int {
	switch u {
	case UpdateMajor:
		return 3
	case UpdateMinor:
		return 2
	case UpdatePatch:
		return 1
	}
	return 0
}

// ClassifyUpdate returns the update type from current to candidate, or
// UpdateNone when candidate is not newer.
func ClassifyUpdate(current, candidate Version) UpdateType {
	if candidate.Compare(current) <= 0 {
		return UpdateNone
	}
	cM, cm, _, ok1 := current.Segments()
	nM, nm, _, ok2 := candidate.Segments()
	switch {
	case !ok1 || !ok2 || nM != cM:
		return UpdateMajor
	case nm != cm:
		return UpdateMinor
	default:
		return UpdatePatch
	}
}
