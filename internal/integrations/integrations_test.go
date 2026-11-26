package integrations

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const sarifOut = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[{"ruleId":"r1","level":"error","message":{"text":"bad"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"api/Main.java"},"region":{"startLine":3}}}]}]}]}`

func sections(t *testing.T, src string) map[string]yaml.Node {
	t.Helper()
	doc, err := strictyaml.Parse([]byte(src), "deepward.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Integrations map[string]yaml.Node `yaml:"integrations"`
	}
	if err := strictyaml.Decode(doc, &out, "", "deepward.yaml"); err != nil {
		t.Fatal(err)
	}
	return out.Integrations
}

func registry() *sdk.Registry {
	reg := sdk.NewRegistry()
	_ = reg.Register(&sdktest.FakeOutputFormat{ID: "sarif-format", Format: "sarif"})
	_ = reg.Register(&sdktest.FakeOutputFormat{ID: "cyclonedx-format", Format: "cyclonedx", FactName: "dependency-graph"})
	_ = reg.Register(&sdktest.FakeAnalyzer{ID: "taken"})
	return reg
}

// writeOutput returns a fake tool that writes content to the path that
// follows --output in its arguments (or prints it).
func writeOutput(content string, exit int) *sdktest.FakeTool {
	return &sdktest.FakeTool{ToolName: "semgrep", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		for i, a := range spec.Args {
			if a == "--output" && i+1 < len(spec.Args) {
				_ = os.WriteFile(spec.Args[i+1], []byte(content), 0o600)
			}
		}
		res := sdk.RunResult{Stdout: []byte(content), ExitCode: exit}
		if exit != 0 {
			return res, &sdk.ExitError{Tool: "semgrep", ExitCode: exit}
		}
		return res, nil
	}}
}

func TestAnalyzerIntegration(t *testing.T) {
	plugins, err := Compile(sections(t, `integrations:
  semgrep:
    type: analyzer
    tool: semgrep
    args: [scan, --sarif, --output, "{{.OutputFile}}", "{{.ProjectDir}}"]
    output: sarif
    exitCodes: [0, 1]
    ecosystems: [maven]
    env: {SEMGREP_SEND_METRICS: "off"}
    kind: code-issue
`), "deepward.yaml", registry())
	if err != nil {
		t.Fatal(err)
	}
	a := plugins[0].(*Analyzer)
	tool := writeOutput(sarifOut, 1)
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{Project: &sdk.Project{Ecosystem: "maven"}}); err == nil {
		t.Error("uninitialized integration must fail")
	}
	_ = a.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"semgrep": tool}}})
	ws := &sdk.Workspace{Root: "/repo"}
	project := &sdk.Project{ID: "maven:api", Ecosystem: "maven", Dir: "api"}
	got := sdktest.TestAnalyzer(t, a, sdk.AnalyzeRequest{Project: project, Workspace: ws})
	if len(got) != 1 || got[0].Analyzer != "semgrep" || got[0].Tool != "semgrep" || got[0].Kind != sdk.KindCodeIssue || got[0].Project != "maven:api" {
		t.Fatalf("findings: %+v", got)
	}
	spec := tool.Invocations[0]
	if spec.Args[0] != "scan" || !strings.HasSuffix(spec.Args[3], "output") || spec.Args[4] != "/repo/api" || spec.Dir != "/repo/api" || spec.Env["SEMGREP_SEND_METRICS"] != "off" {
		t.Errorf("invocation: %+v", spec)
	}
	if a.Info().Integration != true || a.Info().Kind != sdk.KindAnalyzer || !strings.Contains(a.Info().Description, "semgrep") || a.Scope() != sdk.ScopeProject || a.Requires() != nil {
		t.Errorf("info: %+v", a.Info())
	}
	if reqs := a.ToolRequirements(); len(reqs) != 1 || reqs[0].Name != "semgrep" {
		t.Error("tool requirements")
	}
	if fs, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{Project: &sdk.Project{Ecosystem: "nuget"}}); fs != nil || err != nil {
		t.Error("other ecosystems are skipped")
	}
}

func TestWorkspaceStdoutAndFailures(t *testing.T) {
	plugins, err := Compile(sections(t, `integrations:
  linter:
    tool: lint
    output: sarif
    stdout: true
    scope: workspace
    description: Company linter
`), "deepward.yaml", registry())
	if err != nil {
		t.Fatal(err)
	}
	a := plugins[0].(*Analyzer)
	tools := &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"lint": writeOutput(sarifOut, 0)}}
	_ = a.Init(context.Background(), &sdktest.FakeHost{ToolSet: tools})
	got, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{Workspace: &sdk.Workspace{Root: "/repo"}})
	if err != nil || len(got) != 1 || a.Scope() != sdk.ScopeWorkspace || a.Info().Description != "Company linter" {
		t.Fatalf("workspace stdout: %v %v", got, err)
	}
	tools.Tools["lint"] = writeOutput(sarifOut, 2)
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{}); err == nil {
		t.Error("disallowed exit code")
	}
	delete(tools.Tools, "lint")
	if _, err := a.Analyze(context.Background(), sdk.AnalyzeRequest{}); !errors.Is(err, sdk.ErrToolNotFound) {
		t.Errorf("missing tool: %v", err)
	}
	// File mode without the tool writing a file.
	fileMode, _ := Compile(sections(t, "integrations:\n  x:\n    tool: lint\n    output: sarif\n"), "", registry())
	fx := fileMode[0].(*Analyzer)
	_ = fx.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"lint": {ToolName: "lint"}}}})
	if _, err := fx.Analyze(context.Background(), sdk.AnalyzeRequest{}); err == nil || !strings.Contains(err.Error(), "wrote no output file") {
		t.Errorf("no output file: %v", err)
	}
	// outputPath pointing into {{.OutputDir}}.
	dirMode, _ := Compile(sections(t, "integrations:\n  d:\n    tool: lint\n    output: sarif\n    args: [\"{{.OutputDir}}\"]\n    outputPath: \"{{.OutputDir}}/bom.sarif\"\n"), "", registry())
	dx := dirMode[0].(*Analyzer)
	dirTool := &sdktest.FakeTool{ToolName: "lint", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		return sdk.RunResult{}, os.WriteFile(spec.Args[0]+"/bom.sarif", []byte(sarifOut), 0o600)
	}}
	_ = dx.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"lint": dirTool}}})
	if got, err := dx.Analyze(context.Background(), sdk.AnalyzeRequest{}); err != nil || len(got) != 1 {
		t.Errorf("outputPath: %v %v", got, err)
	}
	badOut, _ := Compile(sections(t, "integrations:\n  o:\n    tool: lint\n    output: sarif\n    outputPath: \"{{.Nope}}\"\n"), "", registry())
	bo := badOut[0].(*Analyzer)
	_ = bo.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"lint": {ToolName: "lint"}}}})
	if _, err := bo.Analyze(context.Background(), sdk.AnalyzeRequest{}); err == nil || !strings.Contains(err.Error(), "render outputPath") {
		t.Errorf("outputPath template: %v", err)
	}
	// Template referring to an unknown field fails at render time.
	badTmpl, _ := Compile(sections(t, "integrations:\n  y:\n    tool: lint\n    output: sarif\n    args: [\"{{.Nope}}\"]\n"), "", registry())
	by := badTmpl[0].(*Analyzer)
	_ = by.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"lint": {ToolName: "lint"}}}})
	if _, err := by.Analyze(context.Background(), sdk.AnalyzeRequest{}); err == nil || !strings.Contains(err.Error(), "render args[0]") {
		t.Errorf("template error: %v", err)
	}
}

func TestFactProducerIntegration(t *testing.T) {
	plugins, err := Compile(sections(t, `integrations:
  cyclonedx-maven:
    type: fact-producer
    tool: mvn
    args: [-q, "org.cyclonedx:cyclonedx-maven-plugin:2.9.1:makeAggregateBom", "-DoutputDirectory={{.OutputFile}}"]
    output: cyclonedx
    fact: dependency-graph
    ecosystems: [maven]
`), "deepward.yaml", registry())
	if err != nil {
		t.Fatal(err)
	}
	p := plugins[0].(*FactProducer)
	mvn := &sdktest.FakeTool{ToolName: "mvn", RunFunc: func(_ context.Context, spec sdk.RunSpec) (sdk.RunResult, error) {
		out := strings.TrimPrefix(spec.Args[2], "-DoutputDirectory=")
		return sdk.RunResult{}, os.WriteFile(out, []byte(`{"graph":true}`), 0o600)
	}}
	_ = p.Init(context.Background(), &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"mvn": mvn}}})
	project := sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: "."}
	v := sdktest.TestFactProducer(t, p, sdk.FactRequest{Name: "dependency-graph", Project: project, Workspace: &sdk.Workspace{Root: "/repo"}})
	if v.(map[string]any)["graph"] != true || p.Produces()[0] != "dependency-graph" || p.AppliesTo(sdk.Project{Ecosystem: "sbt"}) || p.Info().Kind != sdk.KindFactProducer {
		t.Errorf("fact: %v", v)
	}
	nilFact := &FactProducer{runner: &runner{id: "n", cfg: Config{Tool: "mvn", Output: "x", Fact: "g", ExitCodes: []int{0}, Stdout: true}, format: &nilFormat{}, host: &sdktest.FakeHost{ToolSet: &sdktest.FakeTools{Tools: map[string]*sdktest.FakeTool{"mvn": {ToolName: "mvn"}}}}}}
	if _, err := nilFact.Produce(context.Background(), sdk.FactRequest{Project: project}); err == nil || !strings.Contains(err.Error(), "returned no g fact") {
		t.Errorf("nil fact: %v", err)
	}
	mvn.RunFunc = func(context.Context, sdk.RunSpec) (sdk.RunResult, error) { return sdk.RunResult{}, errors.New("boom") }
	if _, err := p.Produce(context.Background(), sdk.FactRequest{Project: project}); err == nil {
		t.Error("tool error")
	}
}

func TestCompileErrors(t *testing.T) {
	cases := map[string]string{
		"integrations:\n  x:\n    tool: t\n    output: spdx\n":                                        `integrations.x: unknown output format "spdx" (available: cyclonedx, sarif)`,
		"integrations:\n  x:\n    output: sarif\n":                                                    "tool is required",
		"integrations:\n  Bad_ID:\n    tool: t\n    output: sarif\n":                                  "not kebab-case",
		"integrations:\n  taken:\n    tool: t\n    output: sarif\n":                                   "already used by a plugin",
		"integrations:\n  x:\n    tool: t\n    output: sarif\n    args: [\"{{\"]\n":                   "args[0]",
		"integrations:\n  x:\n    tool: t\n    output: cyclonedx\n":                                   "does not produce findings",
		"integrations:\n  x:\n    tool: t\n    output: sarif\n    scope: repo\n":                      "scope must be project or workspace",
		"integrations:\n  x:\n    type: fact-producer\n    tool: t\n    output: cyclonedx\n":          "fact is required",
		"integrations:\n  x:\n    type: fact-producer\n    tool: t\n    output: sarif\n    fact: g\n": "does not produce the g fact",
		"integrations:\n  x:\n    type: linter\n    tool: t\n    output: sarif\n":                     "type must be analyzer or fact-producer",
		"integrations:\n  x:\n    tool: t\n    output: sarif\n    bogus: 1\n":                         `integrations.x.bogus: unknown key "bogus"`,
		"integrations:\n  x:\n    tool: t\n    output: sarif\n    outputPath: \"{{\"\n":               "outputPath",
	}
	for src, want := range cases {
		_, err := Compile(sections(t, src), "deepward.yaml", registry())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", src, err, want)
		}
	}
	if ps, err := Compile(nil, "", registry()); err != nil || len(ps) != 0 {
		t.Error("no integrations")
	}
}

type nilFormat struct{ sdktest.FakeOutputFormat }

func (*nilFormat) Parse(context.Context, io.Reader, sdk.ParseRequest) (sdk.ParseResult, error) {
	return sdk.ParseResult{}, nil
}
