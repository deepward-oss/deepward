package sarif

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestGoldenAndSchema(t *testing.T) {
	sdktest.TestReporter(t, Reporter{}, sdktest.SampleResult())
	var out bytes.Buffer
	if err := (Reporter{}).Render(context.Background(), sdktest.SampleResult(), &out); err != nil {
		t.Fatal(err)
	}
	if *update {
		_ = os.WriteFile("testdata/golden.sarif", out.Bytes(), 0o644)
	}
	want, err := os.ReadFile("testdata/golden.sarif")
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Error("output differs from testdata/golden.sarif")
	}
	schemaFile, err := os.Open("testdata/sarif-2.1.0.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	defer schemaFile.Close()
	schemaDoc, err := jsonschema.UnmarshalJSON(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("sarif.json", schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("sarif.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("SARIF does not validate against the official 2.1.0 schema: %v", err)
	}
	var log struct {
		Runs []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct{ URI string } `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				Suppressions []struct{ Justification string } `json:"suppressions"`
			} `json:"results"`
			Properties map[string]any `json:"properties"`
		} `json:"runs"`
	}
	_ = json.Unmarshal(out.Bytes(), &log)
	suppressed := 0
	for _, r := range log.Runs[0].Results {
		if len(r.Suppressions) > 0 {
			suppressed++
		}
		if r.RuleID == "CVE-2021-44228" && r.Locations[0].PhysicalLocation.ArtifactLocation.URI != "core/pom.xml" {
			t.Error("results must point to the declaring manifest, relative to the repository root")
		}
	}
	if suppressed != 1 || log.Runs[0].Properties["dataSources"] == nil || log.Runs[0].Properties["commit"] != "0a1b2c3" {
		t.Errorf("suppressions/properties: %d %+v", suppressed, log.Runs[0].Properties)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Reporter{}).Render(ctx, sdktest.SampleResult(), &out); err == nil {
		t.Error("cancelled")
	}
	empty := sdktest.SampleResult()
	empty.DataSources, empty.Run.Commit = nil, ""
	out.Reset()
	_ = Reporter{}.Render(context.Background(), empty, &out)
	if strings.Contains(out.String(), `"dataSources"`) {
		t.Error("no run properties when there is nothing to report")
	}
}
