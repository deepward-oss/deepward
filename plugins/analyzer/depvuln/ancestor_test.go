package depvuln

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

const (
	kafka   = "pkg:maven/org.apache.kafka/kafka-clients@4.4.0"
	lz4     = "pkg:maven/at.yawk.lz4/lz4-java@1.11.2"
	commons = "pkg:maven/com.acme/commons@2.2.0"
	curator = "pkg:maven/org.apache.curator/curator-client@5.9.0"
	zk      = "pkg:maven/org.apache.zookeeper/zookeeper@3.9.3"
)

// kafkaGraph has lz4 reachable from two direct dependencies: kafka-clients
// (one step, declared directly) and com.acme:commons (through kafka-clients).
// commons sorts first alphabetically; kafka-clients is the closer ancestor.
func kafkaGraph(t *testing.T) *deps.Graph {
	t.Helper()
	node := func(id, name, version string) deps.Node {
		return deps.Node{ID: id, Ecosystem: deps.Maven, Name: name, Version: version, Scope: "compile"}
	}
	g, err := deps.NewGraph(root, "test", []deps.Node{
		node(kafka, "org.apache.kafka:kafka-clients", "4.4.0"), node(lz4, "at.yawk.lz4:lz4-java", "1.11.2"),
		node(commons, "com.acme:commons", "2.2.0"), node(curator, "org.apache.curator:curator-client", "5.9.0"),
		node(zk, "org.apache.zookeeper:zookeeper", "3.9.3"),
	}, []deps.Edge{{From: root, To: kafka}, {From: kafka, To: lz4}, {From: root, To: commons}, {From: commons, To: kafka},
		{From: commons, To: curator}, {From: curator, To: zk}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func kafkaAdvisories() []deps.Advisory {
	return []deps.Advisory{
		{ID: "CVE-LZ4-1", DatabaseSeverity: "HIGH", Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "at.yawk.lz4:lz4-java", Ranges: rng("0", "1.11.4")}}},
		{ID: "CVE-LZ4-2", DatabaseSeverity: "LOW", Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "at.yawk.lz4:lz4-java", Ranges: rng("1.7.0", "1.11.3")}}},
		{ID: "CVE-ZK-1", DatabaseSeverity: "HIGH", Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "org.apache.zookeeper:zookeeper", Ranges: rng("3.9.0", "3.9.5")}}},
	}
}

const kafkaPkg = "pkg:maven/org.apache.kafka/kafka-clients"

func kafkaAnalyzer(t *testing.T, versions []string, declared map[string]map[string]string) (*Analyzer, *depstest.ListingEcosystem) {
	t.Helper()
	eco := &depstest.ListingEcosystem{
		Ecosystem: depstest.Ecosystem{FakeEcosystem: sdktest.FakeEcosystem{ID: "maven"},
			Registry: &depstest.FakeRegistry{Versions: map[string][]string{kafkaPkg: versions}}},
		Deps: declared,
	}
	a := New()
	host := &sdktest.FakeHost{PluginsByKind: map[sdk.Kind][]sdk.Plugin{
		deps.KindAdvisorySource: {&depstest.FakeSource{ID: "osv", License: "CC-BY-4.0", Advisories: kafkaAdvisories()}},
		sdk.KindEcosystem:       {eco},
	}}
	if err := a.Init(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	return a, eco
}

func kafkaRequest(t *testing.T) sdk.AnalyzeRequest {
	pom := "<project>\n  <dependencies>\n    <artifactId>commons</artifactId>\n    <artifactId>kafka-clients</artifactId>\n"
	ws := &sdk.Workspace{Root: "/repo", FS: fstest.MapFS{"pom.xml": {Data: []byte(pom)}}}
	return sdk.AnalyzeRequest{Project: &sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml"}}, Workspace: ws,
		Facts: factsOf{deps.FactDependencyGraph: kafkaGraph(t)}}
}

func lz4Deps(m map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for v, l := range m {
		out[kafkaPkg+"@"+v] = map[string]string{"at.yawk.lz4:lz4-java": l, "org.slf4j:slf4j-api": "2.0.0"}
	}
	return out
}

func remediationTargets(f sdk.Finding) string {
	var tos []string
	for _, r := range f.Remediations {
		p := r.Package
		tos = append(tos, strings.TrimPrefix(p.PURL, "pkg:maven/")+" "+p.To+map[bool]string{true: " (pin)"}[p.Pin])
	}
	return strings.Join(tos, ", ")
}

func TestAncestorUpgradeFromPublishedDependencies(t *testing.T) {
	a, eco := kafkaAnalyzer(t, []string{"4.3.0", "4.4.0", "4.4.1", "4.5.0", "4.6.0", "5.0.0-rc1"},
		lz4Deps(map[string]string{"4.4.1": "1.11.3", "4.5.0": "1.11.4", "4.6.0": "1.11.5"}))
	found := sdktest.TestAnalyzer(t, a, kafkaRequest(t))
	m := byRule(found)
	l := m["CVE-LZ4-1"]
	if got := l.Properties["ancestors"].([]string); strings.Join(got, ",") != "org.apache.kafka:kafka-clients@4.4.0,com.acme:commons@2.2.0" {
		t.Errorf("closest ancestor first: %v", got)
	}
	// 4.4.1 brings 1.11.3, which CVE-LZ4-1 still affects: the oldest fixing release is 4.5.0.
	up, _ := l.Properties["ancestorUpgrade"].(map[string]any)
	if up["package"] != "org.apache.kafka:kafka-clients" || up["from"] != "4.4.0" || up["to"] != "4.5.0" || up["brings"] != "1.11.4" {
		t.Errorf("ancestorUpgrade: %v", l.Properties["ancestorUpgrade"])
	}
	if got := remediationTargets(l); got != "org.apache.kafka/kafka-clients 4.5.0, at.yawk.lz4/lz4-java 1.11.4 (pin)" {
		t.Errorf("remediations: %s", got)
	}
	// The upgrade of the parent affects it and what it brings; the pin of
	// the leaf affects only the leaf.
	if got := strings.Join(l.Remediations[0].Affects, ","); got != "pkg:maven/at.yawk.lz4/lz4-java,pkg:maven/org.apache.kafka/kafka-clients" {
		t.Errorf("upgrade affects: %s", got)
	}
	if got := strings.Join(l.Remediations[1].Affects, ","); got != "pkg:maven/at.yawk.lz4/lz4-java" {
		t.Errorf("pin affects: %s", got)
	}
	rec := l.Properties["recommendation"].(string)
	if !strings.HasPrefix(rec, "upgrade org.apache.kafka:kafka-clients from 4.4.0 to 4.5.0: it brings at.yawk.lz4:lz4-java 1.11.4") {
		t.Errorf("recommendation: %s", rec)
	}
	if l.PrimaryFile().StartLine != 4 {
		t.Errorf("declared at the kafka-clients line: %+v", l.PrimaryFile())
	}
	if remediationTargets(m["CVE-LZ4-2"]) != remediationTargets(l) {
		t.Error("advisories of one package share the remediation, so deepward fix groups them")
	}
	// zookeeper comes only through com.acme:commons, which no registry knows.
	z := m["CVE-ZK-1"]
	if got := remediationTargets(z); got != "org.apache.zookeeper/zookeeper 3.9.5 (pin)" {
		t.Errorf("unavailable ancestor: %s", got)
	}
	if rec := z.Properties["recommendation"].(string); !strings.HasPrefix(rec, "Pin org.apache.zookeeper:zookeeper to 3.9.5: deepward could not check newer versions of com.acme:commons") ||
		!strings.Contains(rec, "plugins.maven.repositories") {
		t.Errorf("unavailable recommendation: %s", rec)
	}
	if _, ok := z.Properties["ancestorUpgrade"]; ok {
		t.Error("no ancestorUpgrade without a found version")
	}
	if eco.Calls.Load() == 0 {
		t.Error("published dependencies were not read")
	}
}

func TestAncestorUpgradeOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		versions []string
		declared map[string]string
		targets  string
		rec      string
	}{
		{"no release brings a fix", []string{"4.4.0", "4.4.1", "4.5.0"}, map[string]string{"4.4.1": "1.11.2", "4.5.0": "1.11.3"},
			"at.yawk.lz4/lz4-java 1.11.4 (pin)",
			"Pin at.yawk.lz4:lz4-java to 1.11.4: no released version of org.apache.kafka:kafka-clients brings a fixed at.yawk.lz4:lz4-java yet (the newest, 4.5.0, brings 1.11.3)"},
		{"already newest", []string{"4.3.0", "4.4.0"}, nil,
			"at.yawk.lz4/lz4-java 1.11.4 (pin)",
			"Pin at.yawk.lz4:lz4-java to 1.11.4: org.apache.kafka:kafka-clients 4.4.0 is the newest release"},
		// The newest POM cannot be read: fall back to candidates for deepward fix to verify.
		{"unknown", []string{"4.4.0", "4.4.1", "4.5.0"}, map[string]string{"4.4.1": "1.11.4"},
			"org.apache.kafka/kafka-clients 4.4.1, org.apache.kafka/kafka-clients 4.5.0, at.yawk.lz4/lz4-java 1.11.4 (pin)",
			"upgrade org.apache.kafka:kafka-clients to a version that brings at.yawk.lz4:lz4-java ≥ 1.11.4 (deepward fix tries newer versions and verifies them)"},
		{"newest declares no version", []string{"4.4.0", "4.5.0"}, map[string]string{"4.5.0": ""},
			"org.apache.kafka/kafka-clients 4.5.0, at.yawk.lz4/lz4-java 1.11.4 (pin)", "deepward fix tries newer versions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := kafkaAnalyzer(t, c.versions, lz4Deps(c.declared))
			found, err := a.Analyze(context.Background(), kafkaRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			l := byRule(found)["CVE-LZ4-1"]
			if got := remediationTargets(l); got != c.targets {
				t.Errorf("remediations: %s", got)
			}
			for _, r := range l.Remediations {
				want := "pkg:maven/at.yawk.lz4/lz4-java"
				if !r.Package.Pin {
					want += ",pkg:maven/org.apache.kafka/kafka-clients"
				}
				if got := strings.Join(r.Affects, ","); got != want {
					t.Errorf("%s affects %s", r.Description, got)
				}
			}
			if rec := l.Properties["recommendation"].(string); !strings.Contains(rec, c.rec) {
				t.Errorf("recommendation: %s", rec)
			}
		})
	}
}

func TestProbeEdgeCases(t *testing.T) {
	g := kafkaGraph(t)
	project := sdk.Project{ID: "maven:."}
	n, _ := g.Node(lz4)
	anc, _ := g.Node(kafka)
	if p := probeAncestor(context.Background(), project, g, nil, anc, n, nil); p.outcome != outcomeUnknown {
		t.Error("no registry: unknown")
	}
	eco := &depstest.ListingEcosystem{Ecosystem: depstest.Ecosystem{Registry: &depstest.FakeRegistry{Versions: map[string][]string{kafkaPkg: {"5.0"}}}}}
	if p := probeAncestor(context.Background(), project, g, eco, n, anc, nil); p.outcome != outcomeUnknown {
		t.Error("unreachable package: unknown")
	}
	bad := anc
	bad.Ecosystem = deps.Ecosystem("unsupported")
	if p := probeAncestor(context.Background(), project, g, eco, bad, n, nil); p.outcome != outcomeUnknown {
		t.Error("unparsable ancestor version: unknown")
	}
	if p := upgradeAncestor(context.Background(), project, g, eco, n, []string{"pkg:maven/x/missing@1"}, nil); p.ancestor.ID != "" {
		t.Error("unknown ancestors are skipped")
	}
	if capitalize("") != "" || capitalize("Pin") != "Pin" || capitalize("pin") != "Pin" {
		t.Error("capitalize")
	}
	if got := probeRecommendation(probe{outcome: outcomeUnavailable, ancestor: anc, err: context.DeadlineExceeded}, n, deps.MustParseVersion(deps.Maven, "1.11.4"), "maven"); !strings.Contains(got, "could not be read") {
		t.Errorf("unavailable with an error: %s", got)
	}
}
