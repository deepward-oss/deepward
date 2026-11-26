package sarifimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const semgrep = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"Semgrep OSS"}},"results":[
 {"ruleId":"java.lang.security.audit.xxe","level":"error","message":{"text":"XXE"},
  "locations":[{"physicalLocation":{"artifactLocation":{"uri":"core/Parser.java"},"region":{"startLine":42}}}]},
 {"ruleId":"java.lang.style","level":"note","message":{"text":"style"}}]}]}`

func TestImport(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "semgrep.sarif"), []byte(semgrep), 0o644)
	_ = os.WriteFile(filepath.Join(root, "invalid.sarif"), []byte(`{"version":"2.0.0","runs":[]}`), 0o644)
	a := New()
	a.cfg.Files = map[string]string{"semgrep": "semgrep.sarif"}
	ws := sdk.NewWorkspace(root, nil)
	found := sdktest.TestAnalyzer(t, a, sdk.AnalyzeRequest{Workspace: ws})
	if len(found) != 2 || found[0].Analyzer != ID || found[0].Tool != "semgrep" || found[0].Rule != "java.lang.security.audit.xxe" ||
		found[0].Severity != sdk.SeverityHigh || found[0].PrimaryFile().StartLine != 42 || found[0].Kind != sdk.KindCodeIssue {
		t.Fatalf("findings: %+v", found)
	}
	a.cfg.Files["broken"] = "invalid.sarif"
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{Workspace: ws}); err == nil || !strings.Contains(err.Error(), "import broken (invalid.sarif): invalid SARIF") {
		t.Errorf("invalid file: %v", err)
	}
	a.cfg.Files = map[string]string{"x": filepath.Join(root, "missing.sarif")}
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{}); err == nil {
		t.Error("missing file")
	}
	_ = os.WriteFile(filepath.Join(root, "bag.sarif"), []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"r","message":{"text":"m"},"properties":{"deepward":"bad"}}]}]}`), 0o644)
	a.cfg.Files = map[string]string{"x": "bag.sarif"}
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{Workspace: ws}); err == nil {
		t.Error("bad deepward properties")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Analyze(ctx, sdk.AnalyzeRequest{Workspace: ws}); err == nil {
		t.Error("cancelled")
	}
	if a.Scope() != sdk.ScopeWorkspace || a.Requires() != nil || a.ConfigTarget() == nil {
		t.Error("metadata")
	}
}
