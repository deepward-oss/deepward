// Package osvschema decodes records in the OSV schema
// (https://ossf.github.io/osv-schema/) into deps.Advisory. It is shared by
// the osv and osv-offline advisory sources.
package osvschema

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// Vulnerability is an OSV record (the subset deepward uses).
type Vulnerability struct {
	ID        string     `json:"id"`
	Aliases   []string   `json:"aliases"`
	Summary   string     `json:"summary"`
	Details   string     `json:"details"`
	Modified  time.Time  `json:"modified"`
	Published time.Time  `json:"published"`
	Withdrawn *time.Time `json:"withdrawn"`
	Severity  []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Type   string       `json:"type"`
			Events []deps.Event `json:"events"`
		} `json:"ranges"`
		Versions []string `json:"versions"`
	} `json:"affected"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

// Decode parses one OSV JSON record.
func Decode(data []byte) (*Vulnerability, error) {
	var v Vulnerability
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Ecosystem maps an OSV ecosystem name ("Maven", "NuGet", "npm", "Go",
// "crates.io", or a variant such as "Maven:central") to a supported ecosystem.
func Ecosystem(name string) (deps.Ecosystem, bool) {
	base, _, _ := strings.Cut(name, ":")
	switch strings.ToLower(base) {
	case "maven":
		return deps.Maven, true
	case "nuget":
		return deps.NuGet, true
	case "npm":
		return deps.NPM, true
	case "go":
		return deps.Go, true
	case "crates.io":
		return deps.CratesIO, true
	}
	return "", false
}

// ToAdvisory converts the record. Affected entries of unsupported
// ecosystems are dropped.
func (v *Vulnerability) ToAdvisory(source string) deps.Advisory {
	a := deps.Advisory{ID: v.ID, Aliases: v.Aliases, Summary: v.Summary, Details: v.Details,
		Published: v.Published, Modified: v.Modified, Source: source, DatabaseSeverity: v.DatabaseSpecific.Severity}
	for _, s := range v.Severity {
		a.Severity = append(a.Severity, deps.Score{Type: s.Type, Score: s.Score})
	}
	for _, af := range v.Affected {
		eco, ok := Ecosystem(af.Package.Ecosystem)
		if !ok {
			continue
		}
		entry := deps.Affected{Ecosystem: eco, Name: af.Package.Name, Versions: af.Versions}
		for _, r := range af.Ranges {
			entry.Ranges = append(entry.Ranges, deps.Range{Type: r.Type, Events: r.Events})
		}
		a.Affected = append(a.Affected, entry)
	}
	for _, r := range v.References {
		a.References = append(a.References, sdk.Reference{Type: r.Type, URL: r.URL})
	}
	return a
}

// Withdrawn reports whether the record was withdrawn.
func (v *Vulnerability) IsWithdrawn() bool { return v.Withdrawn != nil }
