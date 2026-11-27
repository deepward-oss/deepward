package html

import (
	"bytes"
	"context"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func render(t *testing.T, res *sdk.ScanResult) string {
	t.Helper()
	var b bytes.Buffer
	if err := (Reporter{}).Render(context.Background(), res, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestGolden(t *testing.T) {
	sdktest.TestReporter(t, Reporter{}, sdktest.SampleResult())
	out := render(t, sdktest.SampleResult())
	if *update {
		_ = os.WriteFile("testdata/golden.html", []byte(out), 0o644)
	}
	want, err := os.ReadFile("testdata/golden.html")
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if out != string(want) {
		t.Error("output differs from testdata/golden.html")
	}
}

// TestSelfContained checks that the report loads no external resources:
// no external scripts, stylesheets, images, fonts or CSS imports.
func TestSelfContained(t *testing.T) {
	out := render(t, sdktest.SampleResult())
	for _, re := range []string{`<script[^>]+src=`, `<link[^>]+href=`, `<img[^>]+src=`, `<iframe`, `url\(`, `@import`, `@font-face`} {
		if regexp.MustCompile(`(?i)` + re).MatchString(out) {
			t.Errorf("report references an external resource (%s)", re)
		}
	}
	if !strings.Contains(out, "<style>") || !strings.Contains(out, "<script>") {
		t.Error("styles and scripts must be inlined")
	}
}

func TestEscapesExternalData(t *testing.T) {
	out := render(t, sdktest.SampleResult())
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Fatal("advisory description injected raw HTML")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("description must be shown literally")
	}
	res := sdktest.SampleResult()
	res.Findings[0].References = []sdk.Reference{{URL: "javascript:alert(1)"}}
	if strings.Contains(render(t, res), `href="javascript:`) {
		t.Error("unsafe URLs must be neutralized")
	}
}

func TestContent(t *testing.T) {
	res := sdktest.SampleResult()
	out := render(t, res)
	for _, want := range []string{
		`data-summary="critical"><span class="num">1</span>`, `data-summary="high"><span class="num">2</span>`,
		"<h2>Vulnerabilities</h2><span class=\"count\" data-count-for=\"vulns\">3</span>", "Outdated dependencies", "Code issue findings",
		"java.lang.security.audit.xxe", "core/src/main/java/Parser.java:42", "semgrep",
		"tool-not-found: sbt", "error NU1101", "plugin panicked: boom", "SnakeYAML Constructor is not used", "2027-01-01",
		`<span class="hop">org.springframework.boot:spring-boot-starter-web<span class="faint">@2.5.0</span></span><span class="sep">→</span><span class="target">com.fasterxml.jackson.core:jackson-databind<span class="faint">@2.12.1</span></span>`,
		"Vulnerability data: <strong>ghsa</strong> (CC-BY-4.0", "Quality gate", "failed", "no fix", "core/pom.xml:14",
		`data-project="maven:."`, `data-direct="transitive"`, `<option value="sbt">sbt</option>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// Vulnerability rows list the severity of each advisory, matching the summary per severity.
	rows := regexp.MustCompile(`<tbody class="item"[^>]*data-findings="([\w ]+)"`).FindAllStringSubmatch(out, -1)
	counts := map[string]int{}
	for _, r := range rows {
		for _, sev := range strings.Fields(r[1]) {
			counts[sev]++
		}
	}
	if counts["critical"] != res.Summary.KindSeverity[sdk.KindVulnerability][sdk.SeverityCritical] ||
		counts["high"] != res.Summary.KindSeverity[sdk.KindVulnerability][sdk.SeverityHigh] {
		t.Errorf("table rows %v vs summary %v", counts, res.Summary.KindSeverity)
	}
}

func TestEmptyReport(t *testing.T) {
	res := &sdk.ScanResult{SchemaVersion: sdk.SchemaVersion}
	res.Normalize()
	out := render(t, res)
	for _, want := range []string{"No vulnerabilities found.", "No projects detected.", "No suppressed findings."} {
		if !strings.Contains(out, want) {
			t.Errorf("empty report lacks %q", want)
		}
	}
	if strings.Contains(out, "Quality gate") || strings.Contains(out, "Outdated dependencies") {
		t.Error("absent sections must not render")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Reporter{}).Render(ctx, res, &bytes.Buffer{}); err == nil {
		t.Error("cancelled")
	}
}

func TestHelpers(t *testing.T) {
	if short("garbage") != "garbage" || short("pkg:maven/a/b") != "a:b" {
		t.Error("short")
	}
	if strs(map[string]any{"x": []any{"a", 1}}, "x")[1] != "1" || strs(map[string]any{}, "x") != nil {
		t.Error("strs")
	}
	if location(sdk.Finding{Project: "p"}) != "p" || location(sdk.Finding{Locations: []sdk.Location{{File: &sdk.FileLocation{Path: "a"}}}}) != "a" {
		t.Error("location")
	}
	f := page.Funcs(nil)
	_ = f
	if kt := page.Lookup("report"); kt == nil {
		t.Error("template")
	}
}

func TestNotBuiltSection(t *testing.T) {
	res := sdktest.SampleResult()
	// Mark the critical vulnerability as not built.
	for i := range res.Findings {
		if res.Findings[i].Kind == sdk.KindVulnerability && res.Findings[i].Severity == sdk.SeverityCritical {
			res.Findings[i].NotBuilt = true
		}
	}
	res.ComputeSummary()
	out := render(t, res)
	for _, want := range []string{
		`<section class="panel notbuilt" id="not-built">`, `data-count-for="vulns-notbuilt">1</span>`, `data-built="not-built"`,
		`<span class="tag notbuilt"`, `data-summary-notbuilt><span class="num">1</span>`,
		`data-summary="critical"><span class="num">0</span>`, `data-count-for="vulns">2</span>`,
		"not compiled into it", `<select id="f-built"`, "findings in packages that are not built do not count toward the gate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	plain := render(t, sdktest.SampleResult())
	for _, unwanted := range []string{`id="not-built"`, `id="f-built"`, "data-summary-notbuilt>"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("report without not-built findings has %q", unwanted)
		}
	}
}

func TestAdvisoriesOfOnePackageShareARow(t *testing.T) {
	res := sdktest.SampleResult()
	var second sdk.Finding
	for _, f := range res.Findings {
		if f.Rule == "CVE-2021-44228" {
			second = f
		}
	}
	second.Fingerprint = ""
	second.Rule, second.Severity, second.Aliases, second.Title = "CVE-2021-45046", sdk.SeverityHigh, nil, "Incomplete fix"
	second.Properties = map[string]any{"package": second.Properties["package"], "fixedIn": "2.17.1", "affected": []string{">=2.0-beta9, <2.16.0"}}
	res.Findings = append(res.Findings, second)
	res.ComputeSummary()
	out := render(t, res)
	if got := strings.Count(out, `<span class="ns">org.apache.logging.log4j:</span><span class="name">log4j-core</span>`); got != 1 {
		t.Errorf("log4j-core rows: %d, want one row for both advisories", got)
	}
	for _, want := range []string{
		`data-severity="critical" data-findings="critical high"`, `</span>2 <span class="muted">advisories</span>`, `title="CVE-2021-44228, CVE-2021-45046"`,
		"Incomplete fix", "&gt;=2.0-beta9, &lt;2.16.0", "4 advisories in 3 package version(s)",
		`data-count-for="vulns">3</span>`, `data-summary="high"><span class="num">3</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// The highest severity comes first inside the row.
	if strings.Index(out, ">CVE-2021-44228</a>") > strings.Index(out, ">CVE-2021-45046</a>") {
		t.Error("advisories are ordered by severity")
	}
}

func TestHelpersForLayout(t *testing.T) {
	for in, want := range map[string][2]string{
		"org.x:lib": {"org.x:", "lib"}, "@scope/pkg": {"@scope/", "pkg"}, "github.com/a/b": {"github.com/a/", "b"},
		"lodash": {"", "lodash"}, "trailing:": {"", "trailing:"},
	} {
		if ns, n := pkgSplit(in); ns != want[0] || n != want[1] {
			t.Errorf("pkgSplit(%q) = %q %q", in, ns, n)
		}
	}
	for in, want := range map[string]string{"critical": "crit", "high": "high", "medium": "med", "low": "low", "unknown": "unkn", "none": "none"} {
		if sevShort(in) != want {
			t.Errorf("sevShort(%q)", in)
		}
	}
	if shortSHA("0123456789abcdef") != "0123456789ab" || shortSHA("abc") != "abc" {
		t.Error("shortSHA")
	}
	if advisoryURL(vulnRow{ID: "X-1"}) != "https://osv.dev/vulnerability/X-1" ||
		advisoryURL(vulnRow{ID: "X-1", References: []sdk.Reference{{URL: "https://example.com/a"}}}) != "https://example.com/a" ||
		advisoryURL(vulnRow{ID: "X-1", References: []sdk.Reference{{URL: "https://example.com/a"}, {URL: "https://osv.dev/vulnerability/X-1"}}}) != "https://osv.dev/vulnerability/X-1" {
		t.Error("advisoryURL")
	}
	if h := hopOf("not a purl", true); h.Name != "not a purl" || !h.Target {
		t.Errorf("hopOf: %+v", h)
	}
}

func TestModulesInReport(t *testing.T) {
	res := sdktest.SampleResult()
	for i := range res.Findings {
		f := &res.Findings[i]
		p := f.PrimaryPackage()
		switch {
		case f.Rule == "CVE-2020-36518":
			p.Modules = []sdk.ModuleRef{{ID: "module:api/pom.xml", Name: "wr-history-api", Scope: "runtime"},
				{ID: "module:app/pom.xml", Name: "wr-history-app", Scope: "runtime"}}
			p.Paths = [][]string{{"module:api/pom.xml", "pkg:maven/org.springframework.boot/spring-boot-starter-web@2.5.0", p.PURL},
				{"module:app/pom.xml", "module:unknown", p.PURL}}
		case f.Kind == sdk.KindOutdated:
			p.Modules = []sdk.ModuleRef{{ID: "module:core/pom.xml", Name: "billing-core", Scope: "compile", Direct: true}}
		}
	}
	out := render(t, res)
	for _, want := range []string{
		`<dt>modules</dt>`, `<span class="mod">wr-history-api</span> <span class="faint">(runtime)</span>`,
		`<li><span class="mod">wr-history-api</span><span class="sep">→</span><span class="hop">org.springframework.boot:spring-boot-starter-web`,
		`<span class="mod">unknown</span>`, "direct · billing-core", "wr-history-app",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if !regexp.MustCompile(`data-search="[^"]*jackson-databind[^"]*wr-history-api`).MatchString(out) {
		t.Error("module names are searchable")
	}
	if !regexp.MustCompile(`data-search="org.yaml:snakeyaml[^"]*billing-core"`).MatchString(out) {
		t.Error("declaring modules of outdated dependencies are searchable")
	}
	if strings.Contains(render(t, sdktest.SampleResult()), "<dt>modules</dt>") {
		t.Error("a result without modules renders no module details")
	}
}
