package sbom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const bom = `{"bomFormat":"CycloneDX","specVersion":"1.5","metadata":{"component":{"bom-ref":"app","name":"api"}},
"components":[{"bom-ref":"l","purl":"pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"}],
"dependencies":[{"ref":"app","dependsOn":["l"]}]}`

func TestImport(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "build"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "build", "bom.json"), []byte(bom), 0o644)
	_ = os.WriteFile(filepath.Join(root, "bad.json"), []byte(`{"bomFormat":"SPDX"}`), 0o644)
	p := New()
	p.cfg.Files = map[string]string{"services/api": "build/bom.json", "bad": "bad.json", "missing": "nope.json", "abs": filepath.Join(root, "build", "bom.json")}
	project := sdk.Project{ID: "maven:services/api", Ecosystem: "maven", Dir: "services/api/"}
	g := sdktest.TestFactProducer(t, p, sdk.FactRequest{Name: deps.FactDependencyGraph, Project: project, Workspace: sdk.NewWorkspace(root, nil)}).(*deps.Graph)
	n, ok := g.Node("pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1")
	if !ok || !n.Direct || g.Source != "cyclonedx:build/bom.json" {
		t.Errorf("graph: %+v", g)
	}
	if p.AppliesTo(sdk.Project{Dir: "other"}) {
		t.Error("only configured projects")
	}
	ws := sdk.NewWorkspace(root, nil)
	for dir, want := range map[string]string{"bad": "not a CycloneDX BOM", "missing": "read SBOM", "other": "no SBOM configured"} {
		_, err := p.Produce(context.Background(), sdk.FactRequest{Project: sdk.Project{ID: dir, Dir: dir}, Workspace: ws})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", dir, err)
		}
	}
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Project: sdk.Project{Dir: "abs"}}); err != nil {
		t.Errorf("absolute path: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Produce(ctx, sdk.FactRequest{Project: project}); err == nil {
		t.Error("cancelled")
	}
	if p.Produces()[0] != deps.FactDependencyGraph || p.ConfigTarget() == nil {
		t.Error("metadata")
	}
}
