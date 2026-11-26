// Package sarif contains a subset of the SARIF 2.1.0 object model and a
// lossless mapping between sdk.Finding and SARIF results.
package sarif

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Version and schema of the produced logs.
const (
	Version = "2.1.0"
	Schema  = "https://json.schemastore.org/sarif-2.1.0.json"
)

// Log is a SARIF log file.
type Log struct {
	Version string `json:"version"`
	Schema  string `json:"$schema,omitempty"`
	Runs    []Run  `json:"runs"`
}

// Run is one tool run.
type Run struct {
	Tool       Tool           `json:"tool"`
	Results    []Result       `json:"results"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Tool describes the producing tool.
type Tool struct {
	Driver Driver `json:"driver"`
}

// Driver is the tool component.
type Driver struct {
	Name           string                `json:"name"`
	Version        string                `json:"version,omitempty"`
	InformationURI string                `json:"informationUri,omitempty"`
	Rules          []ReportingDescriptor `json:"rules,omitempty"`
}

// ReportingDescriptor describes a rule.
type ReportingDescriptor struct {
	ID               string         `json:"id"`
	Name             string         `json:"name,omitempty"`
	ShortDescription *Message       `json:"shortDescription,omitempty"`
	HelpURI          string         `json:"helpUri,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
}

// Result is one finding.
type Result struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level,omitempty"`
	Message             Message           `json:"message"`
	Locations           []Location        `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	Suppressions        []Suppression     `json:"suppressions,omitempty"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

// Suppression marks a result as dismissed.
type Suppression struct {
	Kind          string `json:"kind"`
	Justification string `json:"justification,omitempty"`
}

// Message is a text message.
type Message struct {
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
}

// Location is a result location.
type Location struct {
	PhysicalLocation *PhysicalLocation `json:"physicalLocation,omitempty"`
}

// PhysicalLocation is a file region.
type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           *Region          `json:"region,omitempty"`
}

// ArtifactLocation is a file reference.
type ArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

// Region is a 1-based text region.
type Region struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// FingerprintKey is the partial fingerprint key deepward uses.
const FingerprintKey = "deepward/v1"

const bagKey = "deepward"

// NotBuiltTag is the SARIF result tag of findings marked NotBuilt.
const NotBuiltTag = "not-built"

// bag carries the Finding fields SARIF has no native place for.
type bag struct {
	Analyzer     string                `json:"analyzer"`
	Kind         sdk.FindingKind       `json:"kind"`
	Severity     sdk.Severity          `json:"severity"`
	Title        string                `json:"title,omitempty"`
	Project      string                `json:"project,omitempty"`
	Packages     []sdk.PackageLocation `json:"packages,omitempty"`
	References   []sdk.Reference       `json:"references,omitempty"`
	Aliases      []string              `json:"aliases,omitempty"`
	Tool         string                `json:"tool,omitempty"`
	Properties   map[string]any        `json:"properties,omitempty"`
	Remediations []sdk.Remediation     `json:"remediations,omitempty"`
	Suppression  *sdk.Suppression      `json:"suppression,omitempty"`
	NotBuilt     bool                  `json:"notBuilt,omitempty"`
	// LocationOrder records, for each original location, whether it was a
	// file (f) or package (p) entry so the order is restored exactly.
	LocationOrder string `json:"locationOrder,omitempty"`
}

// Level maps a severity to a SARIF level.
func Level(s sdk.Severity) string {
	switch s.Normalize() {
	case sdk.SeverityCritical, sdk.SeverityHigh:
		return "error"
	case sdk.SeverityMedium:
		return "warning"
	case sdk.SeverityNone:
		return "none"
	default:
		return "note"
	}
}

// SecuritySeverity is the numeric score GitHub code scanning uses to rank
// security results.
func SecuritySeverity(s sdk.Severity) string {
	switch s.Normalize() {
	case sdk.SeverityCritical:
		return "9.5"
	case sdk.SeverityHigh:
		return "8.0"
	case sdk.SeverityMedium:
		return "5.5"
	case sdk.SeverityLow:
		return "2.0"
	}
	return "0.0"
}

// ToResult converts a finding into a SARIF result.
func ToResult(f sdk.Finding) Result {
	b := bag{
		Analyzer: f.Analyzer, Kind: f.Kind, Severity: f.Severity, Title: f.Title, Project: f.Project,
		References: f.References, Aliases: f.Aliases, Tool: f.Tool, Properties: f.Properties,
		Remediations: f.Remediations, Suppression: f.Suppression, NotBuilt: f.NotBuilt,
	}
	r := Result{RuleID: f.Rule, Level: Level(f.Severity), Message: Message{Text: f.Message}}
	var order strings.Builder
	for _, l := range f.Locations {
		switch {
		case l.File != nil:
			order.WriteByte('f')
			pl := &PhysicalLocation{ArtifactLocation: ArtifactLocation{URI: l.File.Path}}
			if l.File.StartLine > 0 || l.File.StartColumn > 0 || l.File.EndLine > 0 || l.File.EndColumn > 0 {
				pl.Region = &Region{StartLine: l.File.StartLine, StartColumn: l.File.StartColumn, EndLine: l.File.EndLine, EndColumn: l.File.EndColumn}
			}
			r.Locations = append(r.Locations, Location{PhysicalLocation: pl})
			if l.Package != nil {
				order.WriteByte('+')
				b.Packages = append(b.Packages, *l.Package)
			}
		case l.Package != nil:
			order.WriteByte('p')
			b.Packages = append(b.Packages, *l.Package)
		}
	}
	b.LocationOrder = order.String()
	if f.Fingerprint != "" {
		r.PartialFingerprints = map[string]string{FingerprintKey: f.Fingerprint}
	}
	r.Properties = map[string]any{
		bagKey:              b,
		"security-severity": SecuritySeverity(f.Severity),
	}
	if f.NotBuilt {
		// SARIF tags let viewers filter findings in code that is not built.
		r.Properties["tags"] = []string{NotBuiltTag}
	}
	return r
}

// FromResult converts a SARIF result into a finding. Results produced by
// deepward round-trip exactly; results from other tools are mapped natively.
func FromResult(r Result, toolName string) (sdk.Finding, error) {
	f := sdk.Finding{Rule: r.RuleID, Message: r.Message.Text, Fingerprint: r.PartialFingerprints[FingerprintKey]}
	if f.Message == "" {
		f.Message = r.Message.ID
	}
	if f.Rule == "" {
		f.Rule = "unknown"
	}
	var files []*sdk.FileLocation
	for _, l := range r.Locations {
		if l.PhysicalLocation == nil {
			continue
		}
		fl := &sdk.FileLocation{Path: l.PhysicalLocation.ArtifactLocation.URI}
		if rg := l.PhysicalLocation.Region; rg != nil {
			fl.StartLine, fl.StartColumn, fl.EndLine, fl.EndColumn = rg.StartLine, rg.StartColumn, rg.EndLine, rg.EndColumn
		}
		files = append(files, fl)
	}
	raw, hasBag := r.Properties[bagKey]
	if !hasBag {
		f.Kind = sdk.KindCodeIssue
		f.Tool = toolName
		f.Severity = severityFromExternal(r)
		for _, fl := range files {
			f.Locations = append(f.Locations, sdk.Location{File: fl})
		}
		if len(r.Properties) > 0 {
			f.Properties = r.Properties
		}
		return f, nil
	}
	var b bag
	if err := remarshal(raw, &b); err != nil {
		return sdk.Finding{}, fmt.Errorf("decode deepward properties: %w", err)
	}
	f.Analyzer, f.Kind, f.Severity, f.Title, f.Project = b.Analyzer, b.Kind, b.Severity, b.Title, b.Project
	f.References, f.Aliases, f.Tool, f.Properties, f.Remediations, f.Suppression = b.References, b.Aliases, b.Tool, b.Properties, b.Remediations, b.Suppression
	f.NotBuilt = b.NotBuilt
	fi, pi := 0, 0
	order := b.LocationOrder
	for i := 0; i < len(order); i++ {
		switch order[i] {
		case 'f':
			if fi >= len(files) {
				return sdk.Finding{}, errors.New("location order does not match locations")
			}
			loc := sdk.Location{File: files[fi]}
			fi++
			if i+1 < len(order) && order[i+1] == '+' {
				if pi >= len(b.Packages) {
					return sdk.Finding{}, errors.New("location order does not match packages")
				}
				p := b.Packages[pi]
				loc.Package = &p
				pi++
				i++
			}
			f.Locations = append(f.Locations, loc)
		case 'p':
			if pi >= len(b.Packages) {
				return sdk.Finding{}, errors.New("location order does not match packages")
			}
			p := b.Packages[pi]
			f.Locations = append(f.Locations, sdk.Location{Package: &p})
			pi++
		default:
			return sdk.Finding{}, fmt.Errorf("invalid location order %q", order)
		}
	}
	return f, nil
}

func severityFromExternal(r Result) sdk.Severity {
	if s, ok := r.Properties["security-severity"]; ok {
		var score float64
		switch v := s.(type) {
		case string:
			score, _ = strconv.ParseFloat(v, 64)
		case float64:
			score = v
		}
		if score > 0 {
			return sdk.SeverityFromCVSS(score)
		}
	}
	switch r.Level {
	case "error":
		return sdk.SeverityHigh
	case "warning", "":
		return sdk.SeverityMedium
	case "note":
		return sdk.SeverityLow
	case "none":
		return sdk.SeverityNone
	}
	return sdk.SeverityUnknown
}

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// FromFindings builds a SARIF log with one run.
func FromFindings(driver Driver, findings []sdk.Finding, runProps map[string]any) Log {
	rules := map[string]ReportingDescriptor{}
	results := make([]Result, 0, len(findings))
	for _, f := range findings {
		results = append(results, ToResult(f))
		if _, ok := rules[f.Rule]; !ok {
			d := ReportingDescriptor{ID: f.Rule}
			if f.Title != "" {
				d.ShortDescription = &Message{Text: f.Title}
			}
			for _, ref := range f.References {
				if ref.URL != "" {
					d.HelpURI = ref.URL
					break
				}
			}
			d.Properties = map[string]any{"security-severity": SecuritySeverity(f.Severity)}
			if f.Kind == sdk.KindVulnerability {
				d.Properties["tags"] = []string{"security", "dependency"}
			}
			rules[f.Rule] = d
		}
	}
	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		driver.Rules = append(driver.Rules, rules[id])
	}
	return Log{Version: Version, Schema: Schema, Runs: []Run{{Tool: Tool{Driver: driver}, Results: results, Properties: runProps}}}
}

// Decode parses and structurally validates a SARIF 2.1.0 log.
func Decode(r io.Reader) (*Log, error) {
	var l Log
	dec := json.NewDecoder(r)
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("invalid SARIF: %w", err)
	}
	if l.Version != Version {
		return nil, fmt.Errorf("invalid SARIF: version %q, expected %s", l.Version, Version)
	}
	if l.Runs == nil {
		return nil, errors.New("invalid SARIF: runs is missing")
	}
	for i, run := range l.Runs {
		if run.Tool.Driver.Name == "" {
			return nil, fmt.Errorf("invalid SARIF: runs[%d].tool.driver.name is missing", i)
		}
		for j, res := range run.Results {
			if res.Message.Text == "" && res.Message.ID == "" {
				return nil, fmt.Errorf("invalid SARIF: runs[%d].results[%d].message is missing", i, j)
			}
		}
	}
	return &l, nil
}

// ToFindings converts every result of a log into findings.
func ToFindings(l *Log, analyzer, tool string) ([]sdk.Finding, error) {
	var out []sdk.Finding
	for _, run := range l.Runs {
		name := tool
		if name == "" {
			name = run.Tool.Driver.Name
		}
		for _, res := range run.Results {
			f, err := FromResult(res, name)
			if err != nil {
				return nil, err
			}
			if f.Analyzer == "" {
				f.Analyzer = analyzer
			}
			out = append(out, f)
		}
	}
	return out, nil
}
