package cyclonedx

import (
	"context"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestFormatConforms(t *testing.T) {
	sample := `{"bomFormat":"CycloneDX","specVersion":"1.5","components":[{"purl":"pkg:maven/org.yaml/snakeyaml@1.33"}]}`
	res := sdktest.TestOutputFormat(t, Format{}, []byte(sample))
	g, ok := res.Fact.(*deps.Graph)
	if !ok || len(g.Packages()) != 1 || g.Source != "cyclonedx:conformance" {
		t.Fatalf("fact: %+v", res.Fact)
	}
	if _, err := (Format{}).Parse(context.Background(), strings.NewReader(`{"bomFormat":"SPDX"}`), sdk.ParseRequest{}); err == nil {
		t.Error("invalid SBOM")
	}
}
