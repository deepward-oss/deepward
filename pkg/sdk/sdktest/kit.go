// Package sdktest is the plugin conformance kit: a public test suite per
// plugin kind that every built-in plugin, every fake and every third-party
// plugin should pass, plus in-memory fakes for engine and plugin tests.
package sdktest

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sarif"
)

// TestPlugin checks the invariants common to every plugin.
func TestPlugin(t *testing.T, p sdk.Plugin) {
	t.Helper()
	info := p.Info()
	if !sdk.ValidID(info.ID) {
		t.Errorf("plugin ID %q is not kebab-case", info.ID)
	}
	if info.Kind == "" {
		t.Errorf("plugin %s has no kind", info.ID)
	}
	if info.Version == "" {
		t.Errorf("plugin %s has no version", info.ID)
	}
	if info.Description == "" {
		t.Errorf("plugin %s has no description", info.ID)
	}
	if !reflect.DeepEqual(info, p.Info()) {
		t.Errorf("plugin %s: Info() is not stable", info.ID)
	}
	if c, ok := p.(sdk.Configurable); ok {
		target := c.ConfigTarget()
		v := reflect.ValueOf(target)
		if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
			t.Fatalf("plugin %s: ConfigTarget must return a pointer to a struct, got %T", info.ID, target)
		}
		b, err := json.Marshal(target)
		if err != nil {
			t.Fatalf("plugin %s: config defaults are not serializable: %v", info.ID, err)
		}
		fresh := reflect.New(v.Elem().Type()).Interface()
		if err := json.Unmarshal(b, fresh); err != nil {
			t.Fatalf("plugin %s: config does not round-trip: %v", info.ID, err)
		}
		if val, ok := target.(sdk.Validator); ok {
			if err := val.Validate(); err != nil {
				t.Errorf("plugin %s: default config does not validate: %v", info.ID, err)
			}
		}
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func noPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// TestEcosystem checks an ecosystem against a workspace.
func TestEcosystem(t *testing.T, e sdk.Ecosystem, ws *sdk.Workspace) {
	t.Helper()
	TestPlugin(t, e)
	if e.Info().Kind != sdk.KindEcosystem {
		t.Errorf("kind = %s, want ecosystem", e.Info().Kind)
	}
	ctx := context.Background()
	first, err := e.Detect(ctx, ws)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	second, _ := e.Detect(ctx, ws)
	if !reflect.DeepEqual(first, second) {
		t.Error("Detect is not deterministic")
	}
	for _, p := range first {
		if p.Ecosystem != e.Info().ID {
			t.Errorf("project %s: ecosystem %q, want %q", p.ID, p.Ecosystem, e.Info().ID)
		}
		if p.ID != sdk.ProjectID(p.Ecosystem, p.Dir) {
			t.Errorf("project ID %q is not canonical", p.ID)
		}
		if len(p.Manifests) == 0 {
			t.Errorf("project %s has no manifests", p.ID)
		}
	}
	noPanic(t, "Detect with cancelled context", func() { _, _ = e.Detect(cancelled(), ws) })
}

// CheckFindings validates findings produced by a plugin.
func CheckFindings(t *testing.T, analyzerID string, findings []sdk.Finding) {
	t.Helper()
	for i := range findings {
		f := findings[i]
		if err := f.Validate(); err != nil {
			t.Errorf("finding %d: %v", i, err)
		}
		if analyzerID != "" && f.Analyzer != analyzerID {
			t.Errorf("finding %d: analyzer %q, want %q", i, f.Analyzer, analyzerID)
		}
		back, err := sarif.FromResult(roundTripJSON(t, sarif.ToResult(f)), "")
		if err != nil {
			t.Errorf("finding %d: SARIF mapping: %v", i, err)
			continue
		}
		a, _ := json.Marshal(f)
		b, _ := json.Marshal(back)
		if !bytes.Equal(a, b) {
			t.Errorf("finding %d is not SARIF-mappable without loss:\n%s\n%s", i, a, b)
		}
	}
}

func roundTripJSON(t *testing.T, r sarif.Result) sarif.Result {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out sarif.Result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAnalyzer runs an analyzer on req twice and checks its findings.
func TestAnalyzer(t *testing.T, a sdk.Analyzer, req sdk.AnalyzeRequest) []sdk.Finding {
	t.Helper()
	TestPlugin(t, a)
	if a.Info().Kind != sdk.KindAnalyzer {
		t.Errorf("kind = %s, want analyzer", a.Info().Kind)
	}
	if s := a.Scope(); s != sdk.ScopeProject && s != sdk.ScopeWorkspace {
		t.Errorf("invalid scope %q", s)
	}
	ctx := context.Background()
	first, err := a.Analyze(ctx, req)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	second, _ := a.Analyze(ctx, req)
	if !reflect.DeepEqual(first, second) {
		t.Error("Analyze is not deterministic")
	}
	CheckFindings(t, a.Info().ID, first)
	noPanic(t, "Analyze with empty request", func() { _, _ = a.Analyze(ctx, sdk.AnalyzeRequest{}) })
	noPanic(t, "Analyze with cancelled context", func() { _, _ = a.Analyze(cancelled(), req) })
	return first
}

// TestFactProducer checks a fact producer for one request.
func TestFactProducer(t *testing.T, p sdk.FactProducer, req sdk.FactRequest) any {
	t.Helper()
	TestPlugin(t, p)
	if len(p.Produces()) == 0 {
		t.Error("Produces is empty")
	}
	if !p.AppliesTo(req.Project) {
		t.Fatalf("producer does not apply to %s", req.Project.ID)
	}
	ctx := context.Background()
	v, err := p.Produce(ctx, req)
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}
	again, _ := p.Produce(ctx, req)
	if !reflect.DeepEqual(v, again) {
		t.Error("Produce is not deterministic")
	}
	noPanic(t, "Produce with cancelled context", func() { _, _ = p.Produce(cancelled(), req) })
	return v
}

// TestReporter renders an empty and a sample result and checks determinism.
func TestReporter(t *testing.T, r sdk.Reporter, sample *sdk.ScanResult) {
	t.Helper()
	TestPlugin(t, r)
	if r.Format() == "" || r.FileName() == "" {
		t.Error("Format and FileName must be set")
	}
	empty := &sdk.ScanResult{SchemaVersion: sdk.SchemaVersion}
	empty.Normalize()
	for _, res := range []*sdk.ScanResult{empty, sample} {
		if res == nil {
			continue
		}
		var a, b bytes.Buffer
		if err := r.Render(context.Background(), res, &a); err != nil {
			t.Fatalf("Render: %v", err)
		}
		_ = r.Render(context.Background(), res, &b)
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Error("Render is not deterministic")
		}
	}
	noPanic(t, "Render with cancelled context", func() { _ = r.Render(cancelled(), empty, &bytes.Buffer{}) })
}

// TestOutputFormat parses sample output and an empty input.
func TestOutputFormat(t *testing.T, f sdk.OutputFormat, sample []byte) sdk.ParseResult {
	t.Helper()
	TestPlugin(t, f)
	if f.Name() == "" || len(f.Produces()) == 0 {
		t.Error("Name and Produces must be set")
	}
	ctx := context.Background()
	req := sdk.ParseRequest{Integration: "conformance", Tool: "conformance"}
	res, err := f.Parse(ctx, bytes.NewReader(sample), req)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	again, _ := f.Parse(ctx, bytes.NewReader(sample), req)
	if !reflect.DeepEqual(res, again) {
		t.Error("Parse is not deterministic")
	}
	CheckFindings(t, "", res.Findings)
	noPanic(t, "Parse of empty input", func() { _, _ = f.Parse(ctx, bytes.NewReader(nil), req) })
	return res
}

// TestToolResolver checks a resolver for a request.
func TestToolResolver(t *testing.T, r sdk.ToolResolver, req sdk.ResolveRequest) *sdk.ToolCandidate {
	t.Helper()
	TestPlugin(t, r)
	c, err := r.Resolve(context.Background(), req)
	if err != nil {
		return nil
	}
	if c != nil && len(c.Command) == 0 {
		t.Error("candidate has an empty command")
	}
	noPanic(t, "Resolve with empty request", func() { _, _ = r.Resolve(context.Background(), sdk.ResolveRequest{}) })
	return c
}

// TestPlatform checks basic platform behavior with a connected client.
func TestPlatform(t *testing.T, p sdk.Platform, cfg sdk.PlatformConfig) sdk.PlatformClient {
	t.Helper()
	TestPlugin(t, p)
	noPanic(t, "Detect of empty URL", func() { _ = p.Detect("") })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := p.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := c.ListChangeRequests(ctx, "deepward-conformance/"); err != nil {
		t.Errorf("ListChangeRequests: %v", err)
	}
	return c
}
