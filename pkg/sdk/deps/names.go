package deps

import (
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// NewPURL builds the package URL of a package named as its ecosystem names
// it: "group:artifact" for Maven, the package ID for NuGet, "@scope/name" for
// npm, the module path for Go and the crate name for crates.io.
func NewPURL(eco Ecosystem, name, version string) purl.PURL {
	switch eco {
	case Maven:
		if g, a, ok := strings.Cut(name, ":"); ok {
			return purl.New(purl.TypeMaven, g, a, version)
		}
	case NPM, Go:
		if i := strings.LastIndex(name, "/"); i > 0 {
			return purl.New(eco.PURLType(), name[:i], name[i+1:], version)
		}
	}
	return purl.New(eco.PURLType(), "", name, version)
}

// PackageName returns the ecosystem's name of the package a purl identifies
// (the inverse of NewPURL).
func PackageName(p purl.PURL) string { return p.Coordinates() }

// AdvisoryVersion returns a version in the form vulnerability databases use:
// Go versions without the leading "v", others unchanged.
func AdvisoryVersion(eco Ecosystem, version string) string {
	if eco == Go {
		return strings.TrimPrefix(version, "v")
	}
	return version
}
