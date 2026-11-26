package engine

import (
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

func TestNotBuiltFindingsInGateAndFixes(t *testing.T) {
	notBuiltHigh := sdk.Finding{Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh, Rule: "CVE-1", NotBuilt: true}
	builtMedium := sdk.Finding{Kind: sdk.KindVulnerability, Severity: sdk.SeverityMedium, Rule: "CVE-2"}
	builtHigh := sdk.Finding{Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh, Rule: "CVE-3"}
	if g := evaluateGate([]sdk.Finding{notBuiltHigh, builtMedium}, sdk.SeverityHigh); g.Failed || g.Count != 0 {
		t.Errorf("not-built findings must not fail the gate: %+v", g)
	}
	if g := evaluateGate([]sdk.Finding{notBuiltHigh, builtMedium}, sdk.SeverityMedium); !g.Failed || g.Count != 1 {
		t.Errorf("built findings count: %+v", g)
	}
	e := &Engine{}
	changeSet := func(findings ...sdk.Finding) *ChangeSet {
		cs := &ChangeSet{Project: sdk.Project{ID: "go:.", Ecosystem: "go", Dir: "."}, Group: "security/grpc",
			Items: []FixItem{{Kind: sdk.KindVulnerability, Package: "pkg:golang/google.golang.org/grpc", From: "v1.82.0", To: "v1.83.2", Findings: findings}}}
		e.describe(cs)
		return cs
	}
	nb := changeSet(notBuiltHigh)
	if !nb.NotBuilt || nb.Urgent || !nb.Security || nb.MaxSeverity != sdk.SeverityHigh ||
		nb.Title != "Deepward: fix vulnerabilities: google.golang.org/grpc v1.82.0 → v1.83.2 (not built)" {
		t.Errorf("not-built change set: %+v", nb)
	}
	body := Body(*nb)
	if !strings.Contains(body, "**Not built:**") || !strings.Contains(body, "CVE-1 (high, not built)") {
		t.Errorf("body:\n%s", body)
	}
	built := changeSet(builtHigh)
	if built.NotBuilt || !built.Urgent || strings.Contains(built.Title, "not built") || strings.Contains(Body(*built), "Not built") {
		t.Errorf("built change set: %+v", built)
	}
	mixed := changeSet(notBuiltHigh, builtMedium)
	if mixed.NotBuilt || mixed.Urgent {
		t.Errorf("mixed change set: %+v", mixed)
	}
	sets := []ChangeSet{*nb, *changeSet(builtMedium), *built}
	sortChangeSets(sets)
	if sets[2].NotBuilt != true || !sets[0].Urgent {
		t.Errorf("order: %v %v %v", sets[0].Title, sets[1].Title, sets[2].Title)
	}
}
