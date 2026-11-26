package engine

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// applyIgnoreRules marks matching findings as suppressed and returns
// warnings for expired rules.
func applyIgnoreRules(findings []sdk.Finding, rules []IgnoreRule, now time.Time) []sdk.Warning {
	var warns []sdk.Warning
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	active := make([]IgnoreRule, 0, len(rules))
	for _, r := range rules {
		if r.Expires != nil && today.After(r.Expires.Time) {
			warns = append(warns, sdk.Warning{Code: "expired-ignore-rule",
				Message: fmt.Sprintf("ignore rule %s expired on %s and no longer applies (reason was: %s)", r.ID, r.Expires.Format(time.DateOnly), r.Reason)})
			continue
		}
		active = append(active, r)
	}
	for i := range findings {
		f := &findings[i]
		for _, r := range active {
			if matches(r, f) {
				s := &sdk.Suppression{Rule: r.ID, Reason: r.Reason}
				if r.Expires != nil {
					s.Expires = r.Expires.Format(time.DateOnly)
				}
				f.Suppression = s
				break
			}
		}
	}
	return warns
}

func matches(r IgnoreRule, f *sdk.Finding) bool {
	if r.Analyzer != "" && r.Analyzer != f.Analyzer {
		return false
	}
	idMatch := strings.EqualFold(r.ID, f.Rule)
	for _, a := range f.Aliases {
		if strings.EqualFold(r.ID, a) {
			idMatch = true
		}
	}
	if !idMatch {
		return false
	}
	if r.PURL == "" {
		return true
	}
	for _, l := range f.Locations {
		if l.Package == nil {
			continue
		}
		id := l.Package.PURL
		if i := strings.LastIndex(id, "@"); i > strings.LastIndex(id, "/") {
			id = id[:i]
		}
		pattern := strings.TrimSuffix(r.PURL, "@*")
		if strings.EqualFold(pattern, id) {
			return true
		}
		if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(id)); ok {
			return true
		}
	}
	return false
}

// evaluateGate counts active findings at or above the threshold. Findings in
// code that is not built do not count.
func evaluateGate(findings []sdk.Finding, threshold sdk.Severity) sdk.Gate {
	g := sdk.Gate{Threshold: threshold}
	if threshold == "" {
		return g
	}
	for _, f := range findings {
		if f.Suppression == nil && !f.NotBuilt && f.Severity.Normalize().AtLeast(threshold) {
			g.Count++
		}
	}
	g.Failed = g.Count > 0
	return g
}
