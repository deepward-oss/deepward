package sdk

import (
	"fmt"
	"strings"
)

// Severity is the impact level of a finding. The zero value is SeverityUnknown.
type Severity string

// Severity levels, from most to least severe. SeverityNone is used for
// informational findings (for example an available update) and never trips
// the quality gate.
const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityUnknown  Severity = "unknown"
	SeverityNone     Severity = "none"
)

// AllSeverities lists levels from most to least severe.
var AllSeverities = []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityUnknown, SeverityNone}

// Rank returns a sortable rank: higher is more severe. Unknown ranks between
// low and none so that unrated vulnerabilities are not silently ignored by a
// "low" gate.
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityNone:
		return 0
	default:
		return 1
	}
}

// AtLeast reports whether s is at least as severe as threshold.
func (s Severity) AtLeast(threshold Severity) bool {
	if s == SeverityNone {
		return false
	}
	return s.Rank() >= threshold.Rank()
}

// Normalize maps the empty value to SeverityUnknown.
func (s Severity) Normalize() Severity {
	if s == "" {
		return SeverityUnknown
	}
	return s
}

// ParseSeverity parses a severity name (case-insensitive). Common synonyms
// used by advisory databases ("moderate", "important") are accepted.
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SeverityCritical, nil
	case "high", "important":
		return SeverityHigh, nil
	case "medium", "moderate":
		return SeverityMedium, nil
	case "low":
		return SeverityLow, nil
	case "unknown", "":
		return SeverityUnknown, nil
	case "none", "info", "note":
		return SeverityNone, nil
	}
	return SeverityUnknown, fmt.Errorf("unknown severity %q (expected critical, high, medium, low, unknown or none)", s)
}

// UnmarshalText implements encoding.TextUnmarshaler for configuration files.
func (s *Severity) UnmarshalText(b []byte) error {
	v, err := ParseSeverity(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// SeverityFromCVSS maps a CVSS v3/v4 base score to a level.
func SeverityFromCVSS(score float64) Severity {
	switch {
	case score >= 9.0:
		return SeverityCritical
	case score >= 7.0:
		return SeverityHigh
	case score >= 4.0:
		return SeverityMedium
	case score > 0:
		return SeverityLow
	default:
		return SeverityNone
	}
}

// MaxSeverity returns the most severe of the given levels (unknown if empty).
func MaxSeverity(levels ...Severity) Severity {
	best := SeverityUnknown
	found := false
	for _, l := range levels {
		l = l.Normalize()
		if !found || l.Rank() > best.Rank() {
			best, found = l, true
		}
	}
	return best
}
