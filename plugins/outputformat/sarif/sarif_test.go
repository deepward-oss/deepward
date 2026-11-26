package sarif

import (
	"context"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const sample = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[
 {"ruleId":"java.lang.security.audit.xxe","level":"error","message":{"text":"XXE"},
  "locations":[{"physicalLocation":{"artifactLocation":{"uri":"src/Main.java"},"region":{"startLine":42,"startColumn":5}}}]}]}]}`

func TestFormatConforms(t *testing.T) {
	res := sdktest.TestOutputFormat(t, Format{}, []byte(sample))
	if len(res.Findings) != 1 || res.Findings[0].Severity != sdk.SeverityHigh || res.Findings[0].PrimaryFile().StartLine != 42 {
		t.Fatalf("findings: %+v", res.Findings)
	}
	p := &sdk.Project{ID: "maven:api"}
	res, _ = Format{}.Parse(context.Background(), strings.NewReader(sample), sdk.ParseRequest{Integration: "semgrep", Tool: "semgrep", Project: p})
	if res.Findings[0].Project != "maven:api" || res.Findings[0].Analyzer != "semgrep" || res.Findings[0].Tool != "semgrep" {
		t.Errorf("project assignment: %+v", res.Findings[0])
	}
	if _, err := (Format{}).Parse(context.Background(), strings.NewReader("{"), sdk.ParseRequest{}); err == nil {
		t.Error("invalid input")
	}
	bad := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"r","message":{"text":"m"},"properties":{"deepward":"bad"}}]}]}`
	if _, err := (Format{}).Parse(context.Background(), strings.NewReader(bad), sdk.ParseRequest{}); err == nil {
		t.Error("bad properties")
	}
}
