package hello

import (
	"context"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

type facts struct{ tree *sdk.SourceTree }

func (f facts) Fact(context.Context, string) (any, error) { return f.tree, nil }

func TestHelloConforms(t *testing.T) {
	project := &sdk.Project{ID: "maven:api", Ecosystem: "maven", Dir: "api", Manifests: []string{"api/pom.xml"}}
	req := sdk.AnalyzeRequest{Project: project, Facts: facts{&sdk.SourceTree{Dir: "api", Files: []string{"api/pom.xml"}}}}
	got := sdktest.TestAnalyzer(t, New(), req)
	if len(got) != 1 || got[0].Rule != "missing-readme" {
		t.Fatalf("findings: %+v", got)
	}
	req.Facts = facts{&sdk.SourceTree{Dir: "api", Files: []string{"api/README.md", "api/pom.xml"}}}
	if got, _ := New().Analyze(context.Background(), req); len(got) != 0 {
		t.Errorf("README present: %+v", got)
	}
}
