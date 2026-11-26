package engine

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestFixEdgeCases(t *testing.T) {
	ws := fixWorkspace(t, "lib=1.0\n")
	// The analyzer offers an out-of-range text edit and a change that breaks
	// the build (the graph fact fails once the file says "broken").
	analyzer := &sdktest.FakeAnalyzer{ID: "edge", Needs: []string{"graph"}, Func: func(_ context.Context, req sdk.AnalyzeRequest) ([]sdk.Finding, error) {
		raw, _ := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
		if !strings.HasPrefix(string(raw), "lib=1.0") {
			return nil, nil
		}
		return []sdk.Finding{{Rule: "R", Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh, Message: "m",
			Locations: []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/g/lib@1.0"}}},
			Remediations: []sdk.Remediation{
				{Description: "bad edit", TextEdits: []sdk.TextEdit{{Path: "app/versions.txt", Start: 999, End: 1000}}},
				{Description: "breaks build", TextEdits: []sdk.TextEdit{{Path: "app/versions.txt", Start: 0, End: 0, Replacement: "broken\n"}}},
			}}}, nil
	}}
	eco := &sdktest.FakeEcosystem{ID: "maven", Marker: "pom.xml", Facts: map[string]func(context.Context, sdk.FactRequest) (any, error){
		"graph": func(_ context.Context, req sdk.FactRequest) (any, error) {
			raw, _ := fs.ReadFile(req.Workspace.FS, "app/versions.txt")
			if strings.HasPrefix(string(raw), "broken") {
				return nil, &sdk.ExitError{Tool: "mvn", ExitCode: 1}
			}
			return "ok", nil
		}}}
	reg := sdk.NewRegistry()
	_ = reg.Register(eco)
	_ = reg.Register(analyzer)
	e, err := New(context.Background(), Options{Registry: reg, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.PlanFixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Failed) != 1 || !strings.Contains(plan.Failed[0].Reason, "no longer builds") {
		t.Errorf("failed: %+v", plan.Failed)
	}
	if advisoryLink(sdk.Finding{Rule: "X"}) != "X" {
		t.Error("advisory without references")
	}
	missing := sdk.Project{ID: "maven:app", Dir: "app", Manifests: []string{"nope.props"}}
	if _, err := e.newScratch(missing); err == nil {
		t.Error("missing manifest")
	}
	sc, err := e.newScratch(sdk.Project{ID: "maven:app", Dir: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.close()
	if _, err := sc.apply([]sdk.TextEdit{{Path: "missing.txt"}}); err == nil {
		t.Error("apply to a missing file")
	}
	cs := []ChangeSet{{Branch: "b", MaxSeverity: sdk.SeverityLow}, {Branch: "a", MaxSeverity: sdk.SeverityLow}, {Branch: "c", Security: true}}
	sortChangeSets(cs)
	if cs[0].Branch != "c" || cs[1].Branch != "a" {
		t.Errorf("order: %+v", cs)
	}
	if !strings.Contains(commitMessage(ChangeSet{Title: "deepward: x", Items: []FixItem{{Text: true, Package: "edit"}}}), "- edit") {
		t.Error("commit message for text edits")
	}
}
