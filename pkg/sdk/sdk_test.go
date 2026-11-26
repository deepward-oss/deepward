package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

type stubPlugin struct{ info Info }

func (s stubPlugin) Info() Info { return s.info }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"zeta", "alpha", "maven"} {
		kind := KindAnalyzer
		if id == "maven" {
			kind = KindEcosystem
		}
		if err := r.Register(stubPlugin{Info{ID: id, Kind: kind}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Register(stubPlugin{Info{ID: "alpha", Kind: KindAnalyzer}}); err == nil {
		t.Error("duplicate must fail")
	}
	for _, bad := range []string{"Bad", "bad_id", "-x", "x-", "1x", ""} {
		if err := r.Register(stubPlugin{Info{ID: bad, Kind: KindAnalyzer}}); err == nil {
			t.Errorf("id %q must be rejected", bad)
		}
	}
	if err := r.Register(stubPlugin{Info{ID: "nokind"}}); err == nil {
		t.Error("missing kind must fail")
	}
	all := r.All()
	if len(all) != 3 || all[0].Info().ID != "alpha" || all[2].Info().ID != "maven" {
		t.Errorf("All order: %v", all)
	}
	if got := r.ByKind(KindAnalyzer); len(got) != 2 || got[1].Info().ID != "zeta" {
		t.Errorf("ByKind: %v", got)
	}
	if _, ok := r.Get("maven"); !ok {
		t.Error("Get")
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get missing")
	}
}

func TestDefaultRegistry(t *testing.T) {
	Register(stubPlugin{Info{ID: "sdk-test-default", Kind: KindReporter}})
	if _, ok := DefaultRegistry().Get("sdk-test-default"); !ok {
		t.Fatal("not registered")
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register must panic")
		}
	}()
	Register(stubPlugin{Info{ID: "sdk-test-default", Kind: KindReporter}})
}

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"backend/build.sbt":              {Data: []byte("x")},
		"backend/target/x.txt":           {Data: []byte("x")},
		"services/api/pom.xml":           {Data: []byte("x")},
		"examples/demo/pom.xml":          {Data: []byte("x")},
		"client/App.csproj":              {Data: []byte("x")},
		"client/obj/project.assets.json": {Data: []byte("x")},
		"tmp/a/gen.xml":                  {Data: []byte("x")},
	}
}

func TestWorkspaceWalkAndExclude(t *testing.T) {
	ws := &Workspace{Root: "/repo", FS: testFS(), Exclude: []string{"examples/", "tmp/*"}}
	var files []string
	err := ws.Walk(func(rel string, d fs.DirEntry) error {
		if !d.IsDir() {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(files, ",")
	want := "backend/build.sbt,client/App.csproj,services/api/pom.xml"
	if got != want {
		t.Errorf("walk = %s, want %s", got, want)
	}
	if ws.IsExcluded(".") || ws.IsExcluded("") || !ws.IsExcluded("a/node_modules") || !ws.IsExcluded("examples/demo") {
		t.Error("IsExcluded")
	}
	if NewWorkspace(t.TempDir(), nil).FS == nil {
		t.Error("NewWorkspace FS")
	}
	if ProjectID("maven", `services\api/`) != "maven:services/api" || ProjectID("sbt", "") != "sbt:." {
		t.Errorf("ProjectID: %s %s", ProjectID("maven", `services\api/`), ProjectID("sbt", ""))
	}
}

func TestWalkError(t *testing.T) {
	ws := &Workspace{FS: fstest.MapFS{}}
	if err := ws.Walk(func(string, fs.DirEntry) error { return errors.New("boom") }); err == nil {
		t.Error("callback error must propagate")
	}
	ws = &Workspace{FS: errFS{}}
	if err := ws.Walk(func(string, fs.DirEntry) error { return nil }); err == nil {
		t.Error("fs error must propagate")
	}
}

type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errors.New("no fs") }

type mapFacts map[string]any

func (m mapFacts) Fact(_ context.Context, name string) (any, error) {
	v, ok := m[name]
	if !ok {
		return nil, &FactError{Name: name, Reason: "not produced", Err: errors.New("cause")}
	}
	return v, nil
}

func TestFacts(t *testing.T) {
	key := NewFactKey[*SourceTree](FactSourceTree)
	ctx := context.Background()
	tree := &SourceTree{Dir: "."}
	got, err := GetFact(ctx, mapFacts{FactSourceTree: tree}, key)
	if err != nil || got != tree {
		t.Fatalf("GetFact: %v %v", got, err)
	}
	_, err = GetFact(ctx, mapFacts{}, key)
	if !errors.Is(err, ErrFactUnavailable) || !strings.Contains(err.Error(), "not produced: cause") {
		t.Errorf("missing fact err = %v", err)
	}
	var fe *FactError
	if !errors.As(err, &fe) || fe.Unwrap() == nil {
		t.Error("FactError unwrap")
	}
	_, err = GetFact(ctx, mapFacts{FactSourceTree: "wrong"}, key)
	if !errors.Is(err, ErrFactUnavailable) {
		t.Errorf("wrong type err = %v", err)
	}
	_, err = GetFact(ctx, nil, key)
	if !errors.Is(err, ErrFactUnavailable) {
		t.Errorf("nil facts err = %v", err)
	}
	if (&FactError{Name: "x"}).Error() != "fact-unavailable: x" {
		t.Error("plain FactError message")
	}
}

func TestSourceTree(t *testing.T) {
	ws := &Workspace{FS: testFS()}
	tree, err := NewSourceTree(ws, "client")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tree.Files, ",") != "client/App.csproj" {
		t.Errorf("files = %v", tree.Files)
	}
	if _, err := NewSourceTree(ws, "missing"); err == nil {
		t.Error("missing dir must fail")
	}
}

func validFinding() Finding {
	return Finding{
		Analyzer: "dependency-vulnerabilities", Rule: "CVE-2021-44228", Kind: KindVulnerability,
		Severity: SeverityCritical, Message: "log4j-core 2.14.1 is vulnerable", Project: "maven:.",
		Locations: []Location{
			{File: &FileLocation{Path: "pom.xml", StartLine: 10}},
			{Package: &PackageLocation{PURL: "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1", Direct: false}},
		},
		Remediations: []Remediation{{Description: "upgrade", Package: &PackageChange{PURL: "pkg:maven/org.apache.logging.log4j/log4j-core", To: "2.17.2"}}},
	}
}

func TestFindingValidate(t *testing.T) {
	f := validFinding()
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := Finding{Severity: "weird", Locations: []Location{{}, {File: &FileLocation{}}, {Package: &PackageLocation{}}},
		Remediations: []Remediation{{Description: "none"}}}
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"analyzer is empty", "rule is empty", "kind is empty", "message is empty", "unknown severity", "location 0 is empty", "empty file path", "empty purl", "exactly one"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestFindingHelpersAndFingerprint(t *testing.T) {
	f := validFinding()
	if f.PrimaryFile().Path != "pom.xml" || !strings.Contains(f.PrimaryPackage().PURL, "log4j-core") {
		t.Error("primary locations")
	}
	fp := f.ComputeFingerprint()
	upgraded := validFinding()
	upgraded.Locations[1].Package.PURL = "pkg:maven/org.apache.logging.log4j/log4j-core@2.15.0"
	if upgraded.ComputeFingerprint() != fp {
		t.Error("fingerprint must ignore the package version")
	}
	other := validFinding()
	other.Rule = "CVE-2021-45046"
	if other.ComputeFingerprint() == fp {
		t.Error("different rule must change fingerprint")
	}
	fileOnly := Finding{Analyzer: "a", Rule: "r", Locations: []Location{{File: &FileLocation{Path: "x.go", StartLine: 3}}}, Message: "m"}
	bare := Finding{Analyzer: "a", Rule: "r", Message: "m"}
	if fileOnly.ComputeFingerprint() == bare.ComputeFingerprint() {
		t.Error("file location must contribute")
	}
	if (&Finding{}).PrimaryFile() != nil || (&Finding{}).PrimaryPackage() != nil {
		t.Error("empty primaries")
	}
}

func TestScanResultNormalizeAndSummary(t *testing.T) {
	low := Finding{Analyzer: "b", Rule: "r2", Kind: KindOutdated, Severity: SeverityNone, Message: "m", Project: "maven:.",
		Locations: []Location{{Package: &PackageLocation{PURL: "pkg:maven/a/b@1", Direct: true}}}}
	crit := validFinding()
	sup := validFinding()
	sup.Rule = "CVE-1"
	sup.Suppression = &Suppression{Rule: "CVE-1", Reason: "not used"}
	unrated := Finding{Analyzer: "c", Rule: "x", Kind: KindCodeIssue, Message: "m", Aliases: []string{"b", "a"}}
	r := &ScanResult{
		Findings:    []Finding{low, sup, unrated, crit},
		Projects:    []ProjectResult{{Project: Project{ID: "sbt:."}}, {Project: Project{ID: "maven:."}}},
		PluginRuns:  []PluginRun{{Plugin: "b", Project: "2"}, {Plugin: "b", Project: "1"}, {Plugin: "a"}},
		Warnings:    []Warning{{Code: "b"}, {Code: "a", Project: "y"}, {Code: "a", Project: "x", Message: "2"}, {Code: "a", Project: "x", Message: "1"}},
		DataSources: []DataSource{{Name: "osv"}, {Name: "ghsa"}},
	}
	r.Normalize()
	if r.Projects[0].ID != "maven:." || r.PluginRuns[0].Plugin != "a" || r.PluginRuns[1].Project != "1" || r.DataSources[0].Name != "ghsa" {
		t.Error("sorting")
	}
	if r.Warnings[0].Message != "1" || r.Warnings[3].Code != "b" {
		t.Errorf("warnings order: %+v", r.Warnings)
	}
	if r.Findings[0].Project != "" || r.Findings[1].Severity != SeverityCritical {
		t.Errorf("findings order: %+v", r.Findings)
	}
	if r.Findings[0].Severity != SeverityUnknown || r.Findings[0].Aliases[0] != "a" || r.Findings[0].Fingerprint == "" {
		t.Error("finding normalization")
	}
	s := r.Summary
	if s.Total != 3 || s.Suppressed != 1 || s.BySeverity[SeverityCritical] != 1 || s.ByKind[KindOutdated] != 1 ||
		s.ByProject["maven:."] != 2 || s.Direct != 1 || s.Transitive != 1 || s.ByAnalyzer["c"] != 1 ||
		s.KindSeverity[KindVulnerability][SeverityCritical] != 1 {
		t.Errorf("summary: %+v", s)
	}
	if len(r.Active()) != 3 {
		t.Error("active")
	}
	empty := &ScanResult{}
	empty.Normalize()
	b, _ := json.Marshal(empty)
	for _, k := range []string{`"projects":[]`, `"findings":[]`, `"pluginRuns":[]`, `"warnings":[]`, `"dataSources":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("empty result JSON lacks %s: %s", k, b)
		}
	}
}

func TestToolErrors(t *testing.T) {
	e := &ToolError{Tool: "sbt", Reason: ErrToolNotFound}
	if !errors.Is(e, ErrToolNotFound) || e.Error() != "tool-not-found: sbt" {
		t.Errorf("ToolError: %v", e)
	}
	e2 := &ToolError{Tool: "sbt", Reason: ErrToolVersionMismatch, Detail: "sbt 1.3.13 does not satisfy >=1.4"}
	if e2.Error() != "tool-version-mismatch: sbt 1.3.13 does not satisfy >=1.4" {
		t.Errorf("detail message: %v", e2)
	}
	if (&ExitError{Tool: "mvn", ExitCode: 2}).Error() != "mvn exited with code 2" {
		t.Error("ExitError")
	}
}
