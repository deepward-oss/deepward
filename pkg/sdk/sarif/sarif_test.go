package sarif

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

func richFinding() sdk.Finding {
	return sdk.Finding{
		Analyzer: "dependency-vulnerabilities", Rule: "CVE-2021-44228", Kind: sdk.KindVulnerability,
		Severity: sdk.SeverityCritical, Title: "Log4Shell", Message: "log4j-core 2.14.1 is vulnerable",
		Project: "maven:.", Fingerprint: "abc123", Tool: "", Aliases: []string{"GHSA-jfh8-c2jp-5v3q"},
		Locations: []sdk.Location{
			{Package: &sdk.PackageLocation{PURL: "pkg:maven/x/y@1", Paths: [][]string{{"pkg:maven/a/b@1", "pkg:maven/x/y@1"}}}},
			{File: &sdk.FileLocation{Path: "pom.xml", StartLine: 12, StartColumn: 5, EndLine: 12, EndColumn: 30},
				Package: &sdk.PackageLocation{PURL: "pkg:maven/a/b@1", Direct: true}},
			{File: &sdk.FileLocation{Path: "README.md"}},
		},
		References:   []sdk.Reference{{Type: "ADVISORY", URL: "https://osv.dev/vulnerability/GHSA-jfh8-c2jp-5v3q"}},
		Properties:   map[string]any{"cvss": map[string]any{"score": 10.0, "vector": "CVSS:3.1/AV:N"}, "fixedIn": "2.17.1"},
		Remediations: []sdk.Remediation{{Description: "upgrade", Package: &sdk.PackageChange{PURL: "pkg:maven/a/b", To: "2"}}},
		Suppression:  &sdk.Suppression{Rule: "CVE-2021-44228", Reason: "test"},
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRoundTripIsLossless(t *testing.T) {
	in := richFinding()
	res := ToResult(in)
	// Go through JSON, as a real file would.
	var decoded Result
	if err := json.Unmarshal([]byte(jsonOf(t, res)), &decoded); err != nil {
		t.Fatal(err)
	}
	out, err := FromResult(decoded, "")
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, in) != jsonOf(t, out) {
		t.Errorf("round trip mismatch\n in: %s\nout: %s", jsonOf(t, in), jsonOf(t, out))
	}
	if res.Level != "error" || res.PartialFingerprints[FingerprintKey] != "abc123" || len(res.Locations) != 2 {
		t.Errorf("native mapping: %+v", res)
	}
}

func TestExternalResultMapping(t *testing.T) {
	cases := []struct {
		res  Result
		want sdk.Severity
	}{
		{Result{RuleID: "r", Level: "error", Message: Message{Text: "m"}}, sdk.SeverityHigh},
		{Result{RuleID: "r", Level: "warning", Message: Message{Text: "m"}}, sdk.SeverityMedium},
		{Result{RuleID: "r", Message: Message{Text: "m"}}, sdk.SeverityMedium},
		{Result{RuleID: "r", Level: "note", Message: Message{Text: "m"}}, sdk.SeverityLow},
		{Result{RuleID: "r", Level: "none", Message: Message{Text: "m"}}, sdk.SeverityNone},
		{Result{RuleID: "r", Level: "odd", Message: Message{Text: "m"}}, sdk.SeverityUnknown},
		{Result{RuleID: "r", Level: "note", Message: Message{Text: "m"}, Properties: map[string]any{"security-severity": "9.1"}}, sdk.SeverityCritical},
		{Result{RuleID: "r", Level: "note", Message: Message{Text: "m"}, Properties: map[string]any{"security-severity": 7.2}}, sdk.SeverityHigh},
	}
	for i, c := range cases {
		f, err := FromResult(c.res, "semgrep")
		if err != nil {
			t.Fatal(err)
		}
		if f.Severity != c.want || f.Kind != sdk.KindCodeIssue || f.Tool != "semgrep" {
			t.Errorf("case %d: %+v", i, f)
		}
	}
	f, _ := FromResult(Result{Message: Message{ID: "msg-id"}, Locations: []Location{{}, {PhysicalLocation: &PhysicalLocation{
		ArtifactLocation: ArtifactLocation{URI: "src/Main.scala"}, Region: &Region{StartLine: 42, StartColumn: 5}}}}}, "x")
	if f.Rule != "unknown" || f.Message != "msg-id" || f.PrimaryFile().StartLine != 42 || len(f.Locations) != 1 {
		t.Errorf("external locations: %+v", f)
	}
}

func TestFromResultBadBag(t *testing.T) {
	for _, bagVal := range []any{
		"not an object",
		map[string]any{"locationOrder": "f"},
		map[string]any{"locationOrder": "p"},
		map[string]any{"locationOrder": "x"},
	} {
		res := Result{RuleID: "r", Message: Message{Text: "m"}, Properties: map[string]any{"deepward": bagVal}}
		if _, err := FromResult(res, ""); err == nil {
			t.Errorf("bag %v must fail", bagVal)
		}
	}
	res := Result{RuleID: "r", Message: Message{Text: "m"}, Locations: []Location{{PhysicalLocation: &PhysicalLocation{}}},
		Properties: map[string]any{"deepward": map[string]any{"locationOrder": "f+"}}}
	if _, err := FromResult(res, ""); err == nil {
		t.Error("f+ without package must fail")
	}
	if _, err := FromResult(Result{Properties: map[string]any{"deepward": map[string]any{"properties": func() {}}}}, ""); err == nil {
		t.Error("unmarshalable bag must fail")
	}
}

func TestLevelsAndScores(t *testing.T) {
	for sev, want := range map[sdk.Severity][2]string{
		sdk.SeverityCritical: {"error", "9.5"}, sdk.SeverityHigh: {"error", "8.0"}, sdk.SeverityMedium: {"warning", "5.5"},
		sdk.SeverityLow: {"note", "2.0"}, sdk.SeverityUnknown: {"note", "0.0"}, sdk.SeverityNone: {"none", "0.0"},
	} {
		if Level(sev) != want[0] || SecuritySeverity(sev) != want[1] {
			t.Errorf("%s: %s %s", sev, Level(sev), SecuritySeverity(sev))
		}
	}
}

func TestFromFindingsAndDecode(t *testing.T) {
	f2 := richFinding()
	f2.Title = ""
	f2.References = nil
	f2.Rule = "CVE-0"
	f2.Kind = sdk.KindOutdated
	log := FromFindings(Driver{Name: "deepward", Version: "1"}, []sdk.Finding{richFinding(), f2, richFinding()}, map[string]any{"k": "v"})
	if len(log.Runs[0].Tool.Driver.Rules) != 2 || log.Runs[0].Tool.Driver.Rules[0].ID != "CVE-0" {
		t.Errorf("rules: %+v", log.Runs[0].Tool.Driver.Rules)
	}
	if log.Runs[0].Tool.Driver.Rules[1].HelpURI == "" || log.Runs[0].Tool.Driver.Rules[1].ShortDescription.Text != "Log4Shell" {
		t.Error("rule metadata")
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(log)
	parsed, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := ToFindings(parsed, "sarif-import", "")
	if err != nil || len(fs) != 3 || fs[0].Analyzer != "dependency-vulnerabilities" {
		t.Fatalf("ToFindings: %v %v", fs, err)
	}
	ext := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[{"ruleId":"x","message":{"text":"m"}}]}]}`
	parsed, _ = Decode(strings.NewReader(ext))
	fs, _ = ToFindings(parsed, "sarif-import", "")
	if fs[0].Analyzer != "sarif-import" || fs[0].Tool != "semgrep" {
		t.Errorf("external: %+v", fs[0])
	}
	fs, _ = ToFindings(parsed, "sarif-import", "custom")
	if fs[0].Tool != "custom" {
		t.Error("tool override")
	}
	bad := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"x","message":{},"properties":{"deepward":"bad"}}]}]}`
	if _, err := Decode(strings.NewReader(bad)); err == nil {
		t.Error("message missing must fail")
	}
	badBag := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"x","message":{"text":"m"},"properties":{"deepward":"bad"}}]}]}`
	parsed, _ = Decode(strings.NewReader(badBag))
	if _, err := ToFindings(parsed, "a", ""); err == nil {
		t.Error("bad bag must fail ToFindings")
	}
	for _, in := range []string{"{", `{"version":"2.0.0","runs":[]}`, `{"version":"2.1.0"}`, `{"version":"2.1.0","runs":[{"tool":{"driver":{}}}]}`} {
		if _, err := Decode(strings.NewReader(in)); err == nil {
			t.Errorf("Decode(%s) must fail", in)
		}
	}
}

func TestNotBuiltRoundTrip(t *testing.T) {
	in := richFinding()
	in.NotBuilt = true
	res := ToResult(in)
	if tags, _ := res.Properties["tags"].([]string); len(tags) != 1 || tags[0] != NotBuiltTag {
		t.Errorf("tags: %v", res.Properties["tags"])
	}
	var decoded Result
	if err := json.Unmarshal([]byte(jsonOf(t, res)), &decoded); err != nil {
		t.Fatal(err)
	}
	out, err := FromResult(decoded, "")
	if err != nil || !out.NotBuilt || jsonOf(t, in) != jsonOf(t, out) {
		t.Errorf("round trip: %v %v", out.NotBuilt, err)
	}
	if _, ok := ToResult(richFinding()).Properties["tags"]; ok {
		t.Error("built findings have no tag")
	}
}
