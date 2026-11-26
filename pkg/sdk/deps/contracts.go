package deps

import (
	"context"
	"errors"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// KindAdvisorySource is the plugin kind of vulnerability databases. Advisory
// sources are consumed by the dependency-vulnerabilities analyzer.
const KindAdvisorySource sdk.Kind = "advisory-source"

// DataLicense describes the license of a source's data.
type DataLicense struct {
	// SPDX is the license identifier (e.g. "CC-BY-4.0").
	SPDX        string
	Attribution string
	URL         string
}

// PackageQuery asks for advisories of one package version.
type PackageQuery struct {
	Ecosystem Ecosystem
	// Name is the package name as used by advisory databases
	// ("group:artifact" for Maven, the package ID for NuGet).
	Name    string
	Version string
}

// AdvisorySource is a vulnerability database.
type AdvisorySource interface {
	sdk.Plugin
	DataLicense() DataLicense
	// Query returns advisories affecting any of the queried packages. The
	// result may contain advisories that do not affect the exact version;
	// callers match versions themselves.
	Query(ctx context.Context, queries []PackageQuery) ([]Advisory, error)
	// Freshness returns when the data was last updated, if known.
	Freshness() *time.Time
}

// Advisory is a vulnerability record, modelled on the OSV schema.
type Advisory struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Details  string   `json:"details,omitempty"`
	Severity []Score  `json:"severity,omitempty"`
	// DatabaseSeverity is a textual rating from the source (e.g. "HIGH").
	DatabaseSeverity string          `json:"databaseSeverity,omitempty"`
	Affected         []Affected      `json:"affected"`
	References       []sdk.Reference `json:"references,omitempty"`
	Published        time.Time       `json:"published,omitempty"`
	Modified         time.Time       `json:"modified,omitempty"`
	// Source is the advisory source plugin ID.
	Source string `json:"source"`
}

// Score is a severity score such as a CVSS vector.
type Score struct {
	Type  string `json:"type"`  // CVSS_V3, CVSS_V4
	Score string `json:"score"` // the vector string
	// Value is the numeric base score when the source provides it.
	Value float64 `json:"value,omitempty"`
}

// Affected lists affected versions of one package.
type Affected struct {
	Ecosystem Ecosystem `json:"ecosystem"`
	Name      string    `json:"name"`
	Ranges    []Range   `json:"ranges,omitempty"`
	Versions  []string  `json:"versions,omitempty"`
}

// Range is an OSV affected range.
type Range struct {
	Type   string  `json:"type"` // ECOSYSTEM or SEMVER
	Events []Event `json:"events"`
}

// Event is one OSV range event; exactly one field is set.
type Event struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
	Limit        string `json:"limit,omitempty"`
}

// OfflineSource is implemented by advisory sources that work without network
// access; Available reports whether their local data exists.
type OfflineSource interface {
	Available() bool
}

// VersionRegistry lists available versions of packages. Ecosystem plugins
// implement it.
type VersionRegistry interface {
	// Versions returns all published versions of a package (any order). It
	// returns ErrPackageNotFound when no configured registry knows it.
	Versions(ctx context.Context, project sdk.Project, pkg string) ([]string, error)
}

// ReleaseNotesProvider is optionally implemented by version registries.
type ReleaseNotesProvider interface {
	ReleaseNotesURL(ctx context.Context, project sdk.Project, pkg, version string) (string, error)
}

// ErrPackageNotFound is returned by VersionRegistry when no registry has the package.
var ErrPackageNotFound = errors.New("package not found in any registry")
