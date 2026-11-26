package sdk

import "testing"

func TestSummaryNotBuilt(t *testing.T) {
	r := &ScanResult{Findings: []Finding{
		{Kind: KindVulnerability, Severity: SeverityHigh, NotBuilt: true},
		{Kind: KindVulnerability, Severity: SeverityHigh},
		{Kind: KindVulnerability, Severity: SeverityMedium, NotBuilt: true},
		{Kind: KindVulnerability, Severity: SeverityCritical, NotBuilt: true, Suppression: &Suppression{Reason: "x"}},
	}}
	r.ComputeSummary()
	s := r.Summary
	if s.Total != 3 || s.NotBuilt != 2 || s.NotBuiltBySeverity[SeverityHigh] != 1 || s.NotBuiltBySeverity[SeverityMedium] != 1 {
		t.Errorf("summary: %+v", s)
	}
	built := s.BuiltBySeverity()
	if built[SeverityHigh] != 1 || built[SeverityMedium] != 0 || len(built) != 1 {
		t.Errorf("built: %v", built)
	}
	empty := &ScanResult{Findings: []Finding{{Kind: KindVulnerability, Severity: SeverityLow}}}
	empty.ComputeSummary()
	if empty.Summary.NotBuiltBySeverity != nil || empty.Summary.BuiltBySeverity()[SeverityLow] != 1 {
		t.Errorf("no not-built findings: %+v", empty.Summary)
	}
}
