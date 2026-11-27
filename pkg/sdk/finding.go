package sdk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// FindingKind classifies findings. Plugins may define their own kinds.
type FindingKind string

// Well-known finding kinds.
const (
	KindVulnerability FindingKind = "vulnerability"
	KindOutdated      FindingKind = "outdated"
	KindLicense       FindingKind = "license"
	KindCodeIssue     FindingKind = "code-issue"
)

// Finding is the generic result of any analyzer. It maps losslessly to a
// SARIF 2.1.0 result (see package sdk/sarifmap).
type Finding struct {
	Analyzer string      `json:"analyzer"`
	Rule     string      `json:"rule"`
	Kind     FindingKind `json:"kind"`
	Severity Severity    `json:"severity"`
	Title    string      `json:"title,omitempty"`
	Message  string      `json:"message"`
	// Project is the ID of the project the finding belongs to (empty for
	// workspace-level findings).
	Project    string      `json:"project,omitempty"`
	Locations  []Location  `json:"locations,omitempty"`
	References []Reference `json:"references,omitempty"`
	// Aliases are other identifiers of the same rule (e.g. CVE and GHSA IDs).
	Aliases []string `json:"aliases,omitempty"`
	// Fingerprint identifies the finding across runs; the engine computes it
	// when empty.
	Fingerprint string `json:"fingerprint"`
	// Tool is the external tool that produced the finding, if any.
	Tool string `json:"tool,omitempty"`
	// Properties holds kind-specific data (JSON-serializable).
	Properties map[string]any `json:"properties,omitempty"`
	// Remediations are alternative fixes in order of preference.
	Remediations []Remediation `json:"remediations,omitempty"`
	// NotBuilt marks findings in code that is not part of the build (e.g. a
	// dependency that is resolved but not compiled). They keep their severity
	// and remediations, but do not count toward the quality gate, never get
	// urgent priority and are shown apart in reports.
	NotBuilt bool `json:"notBuilt,omitempty"`
	// Suppression is set by the engine when an ignore rule matched.
	Suppression *Suppression `json:"suppression,omitempty"`
}

// Location is a file region and/or a package position in a dependency graph.
type Location struct {
	File    *FileLocation    `json:"file,omitempty"`
	Package *PackageLocation `json:"package,omitempty"`
}

// FileLocation is a region in a file. Lines and columns are 1-based; zero
// means unknown.
type FileLocation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"startLine,omitempty"`
	StartColumn int    `json:"startColumn,omitempty"`
	EndLine     int    `json:"endLine,omitempty"`
	EndColumn   int    `json:"endColumn,omitempty"`
}

// PackageLocation is a package within a project's dependency graph.
type PackageLocation struct {
	PURL string `json:"purl"`
	// Paths are dependency chains from the project root to the package,
	// each as a list of purls.
	Paths  [][]string `json:"paths,omitempty"`
	Direct bool       `json:"direct"`
	Scope  string     `json:"scope,omitempty"`
	// Modules are the modules of a multi-module project that reach the
	// package, sorted by name; empty for projects without modules.
	Modules []ModuleRef `json:"modules,omitempty"`
}

// ModuleRef is a module that reaches a package.
type ModuleRef struct {
	// ID is the module node ID in the dependency graph (as in Paths).
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scope is the package scope in this module.
	Scope string `json:"scope,omitempty"`
	// Direct is set when the module declares the package itself.
	Direct bool `json:"direct"`
}

// Reference is an external link.
type Reference struct {
	Type string `json:"type,omitempty"` // e.g. ADVISORY, FIX, WEB
	URL  string `json:"url"`
}

// Remediation is one way to fix a finding: either a package change realized
// by the owning ecosystem, or direct text edits.
type Remediation struct {
	Description string         `json:"description"`
	Package     *PackageChange `json:"package,omitempty"`
	TextEdits   []TextEdit     `json:"textEdits,omitempty"`
	// Group is a hint for grouping remediations into one change request.
	Group string `json:"group,omitempty"`
	// Affects lists the packages the change may alter, as package URLs
	// without version: for a package change, the package and every package
	// it currently brings in. The fix pipeline verifies changes of different
	// change requests together only when their Affects do not overlap; an
	// empty list means the change is verified on its own.
	Affects []string `json:"affects,omitempty"`
}

// PackageChange sets a package to a version.
type PackageChange struct {
	// PURL identifies the package without version.
	PURL string `json:"purl"`
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	// Pin requests adding a version override for a transitive dependency
	// instead of changing a declared version.
	Pin    bool   `json:"pin,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Targets are the manifests the change should be applied to, for
	// example the project files of the modules through which a pinned
	// package enters the project. It is a hint: ecosystems that pin in one
	// central place ignore it, and empty means the ecosystem decides.
	Targets []string `json:"targets,omitempty"`
}

// TextEdit replaces bytes [Start, End) of a file. Expected, if set, must
// match the current content of that range.
type TextEdit struct {
	Path        string `json:"path"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Replacement string `json:"replacement"`
	Expected    string `json:"expected,omitempty"`
}

// Suppression records which ignore rule matched a finding.
type Suppression struct {
	Rule    string `json:"rule"`
	Reason  string `json:"reason"`
	Expires string `json:"expires,omitempty"`
}

// Validate checks the invariants every analyzer must respect.
func (f *Finding) Validate() error {
	var errs []error
	if f.Analyzer == "" {
		errs = append(errs, errors.New("analyzer is empty"))
	}
	if f.Rule == "" {
		errs = append(errs, errors.New("rule is empty"))
	}
	if f.Kind == "" {
		errs = append(errs, errors.New("kind is empty"))
	}
	if f.Message == "" {
		errs = append(errs, errors.New("message is empty"))
	}
	if _, err := ParseSeverity(string(f.Severity)); err != nil {
		errs = append(errs, err)
	}
	for i, l := range f.Locations {
		if l.File == nil && l.Package == nil {
			errs = append(errs, fmt.Errorf("location %d is empty", i))
		}
		if l.File != nil && l.File.Path == "" {
			errs = append(errs, fmt.Errorf("location %d has an empty file path", i))
		}
		if l.Package != nil && l.Package.PURL == "" {
			errs = append(errs, fmt.Errorf("location %d has an empty purl", i))
		}
	}
	for i, r := range f.Remediations {
		if (r.Package == nil) == (len(r.TextEdits) == 0) {
			errs = append(errs, fmt.Errorf("remediation %d must have exactly one of package or textEdits", i))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid finding %s/%s: %w", f.Analyzer, f.Rule, errors.Join(errs...))
	}
	return nil
}

// PrimaryFile returns the first file location, if any.
func (f *Finding) PrimaryFile() *FileLocation {
	for _, l := range f.Locations {
		if l.File != nil {
			return l.File
		}
	}
	return nil
}

// PrimaryPackage returns the first package location, if any.
func (f *Finding) PrimaryPackage() *PackageLocation {
	for _, l := range f.Locations {
		if l.Package != nil {
			return l.Package
		}
	}
	return nil
}

// ComputeFingerprint returns a stable identity built from analyzer, rule,
// project and primary location. Package versions are excluded so that the
// fingerprint survives upgrades only when the finding is the same issue.
func (f *Finding) ComputeFingerprint() string {
	parts := []string{f.Analyzer, f.Rule, f.Project}
	if p := f.PrimaryPackage(); p != nil {
		id := p.PURL
		if i := strings.LastIndex(id, "@"); i > strings.LastIndex(id, "/") {
			id = id[:i]
		}
		parts = append(parts, id)
	} else if fl := f.PrimaryFile(); fl != nil {
		parts = append(parts, fl.Path, fmt.Sprint(fl.StartLine), f.Message)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:12])
}

// SortFindings orders findings deterministically.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if ra, rb := a.Severity.Rank(), b.Severity.Rank(); ra != rb {
			return ra > rb
		}
		if a.Analyzer != b.Analyzer {
			return a.Analyzer < b.Analyzer
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Fingerprint < b.Fingerprint
	})
}
