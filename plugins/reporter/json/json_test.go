package json

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestGoldenAndDeterminism(t *testing.T) {
	sdktest.TestReporter(t, Reporter{}, sdktest.SampleResult())
	var a, b bytes.Buffer
	if err := (Reporter{}).Render(context.Background(), sdktest.SampleResult(), &a); err != nil {
		t.Fatal(err)
	}
	_ = Reporter{}.Render(context.Background(), sdktest.SampleResult(), &b)
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two renders from the same data differ")
	}
	if *update {
		_ = os.WriteFile("testdata/golden.json", a.Bytes(), 0o644)
	}
	want, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(a.Bytes(), want) {
		t.Error("output differs from testdata/golden.json (run with -update after reviewing)")
	}
	var back sdk.ScanResult
	if err := json.Unmarshal(a.Bytes(), &back); err != nil || back.SchemaVersion != 1 || len(back.Findings) != 6 {
		t.Errorf("round trip: %v %d", err, len(back.Findings))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Reporter{}).Render(ctx, &back, &a); err == nil {
		t.Error("cancelled")
	}
	if (Reporter{}).FileName() != "deepward-report.json" {
		t.Error("file name")
	}
}
