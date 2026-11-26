package domain

import (
	"math"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

const log4j = "org.apache.logging.log4j:log4j-core"

func log4shell() deps.Advisory {
	return deps.Advisory{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Source: "osv",
		Severity: []deps.Score{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H"}},
		Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: log4j, Ranges: []deps.Range{{Type: "ECOSYSTEM",
			Events: []deps.Event{{Introduced: "2.0-beta9"}, {Fixed: "2.15.0"}}}}}}}
}

func v(s string) deps.Version { return deps.MustParseVersion(deps.Maven, s) }

func TestAffects(t *testing.T) {
	adv := log4shell()
	cases := map[string]bool{"2.14.1": true, "2.0-beta9": true, "2.0-alpha1": false, "2.15.0-rc1": true, "2.15.0": false, "2.17.2": false}
	for ver, want := range cases {
		if got := Affects(adv, deps.Maven, log4j, v(ver)); got != want {
			t.Errorf("%s affected = %v, want %v", ver, got, want)
		}
	}
	if Affects(adv, deps.Maven, "other:pkg", v("2.14.1")) || Affects(adv, deps.NuGet, log4j, deps.MustParseVersion(deps.NuGet, "2.14.1")) {
		t.Error("other packages/ecosystems")
	}
	multi := deps.Advisory{Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "g:a", Versions: []string{"1.5"}, Ranges: []deps.Range{
		{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "0"}, {LastAffected: "1.2"}}},
		{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "2.0"}, {Limit: "2.5"}, {}}},
		{Type: "GIT", Events: []deps.Event{{Introduced: "abc"}}},
		{Type: "SEMVER", Events: []deps.Event{{Fixed: "3.1"}, {Introduced: "3.0"}}},
	}}}}
	for ver, want := range map[string]bool{"1.0": true, "1.2": true, "1.3": false, "1.5": true, "2.4": true, "2.5": false, "3.0.5": true, "3.1": false} {
		if got := Affects(multi, deps.Maven, "g:a", v(ver)); got != want {
			t.Errorf("multi %s = %v, want %v", ver, got, want)
		}
	}
	nuget := deps.Advisory{Affected: []deps.Affected{{Ecosystem: "nuget", Name: "Newtonsoft.Json", Ranges: []deps.Range{{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "0"}, {Fixed: "13.0.1"}}}}}}}
	if !Affects(nuget, deps.NuGet, "newtonsoft.json", deps.MustParseVersion(deps.NuGet, "12.0.3")) {
		t.Error("NuGet names and ecosystem are case-insensitive")
	}
}

func TestFixedVersion(t *testing.T) {
	cve2 := deps.Advisory{ID: "CVE-2021-45046", Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: log4j, Ranges: []deps.Range{{Type: "ECOSYSTEM",
		Events: []deps.Event{{Introduced: "2.0-beta9"}, {Fixed: "2.16.0"}}}}}}}
	fixed, ok := FixedVersion([]deps.Advisory{log4shell(), cve2}, deps.Maven, log4j, v("2.14.1"), nil)
	if !ok || fixed.String() != "2.16.0" {
		t.Errorf("fixed = %v %v (2.15.0 is still affected by CVE-2021-45046)", fixed, ok)
	}
	noFix := deps.Advisory{Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "g:a", Ranges: []deps.Range{{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "0"}}}}}}}
	if _, ok := FixedVersion([]deps.Advisory{noFix}, deps.Maven, "g:a", v("1.0"), []string{"2.0", "bad version!!"}); ok {
		t.Error("no fix available")
	}
	lastAffected := deps.Advisory{Affected: []deps.Affected{{Ecosystem: deps.Maven, Name: "g:a", Ranges: []deps.Range{{Type: "ECOSYSTEM", Events: []deps.Event{{Introduced: "0"}, {LastAffected: "1.4"}}}}}}}
	got, ok := FixedVersion([]deps.Advisory{lastAffected}, deps.Maven, "g:a", v("1.0"), []string{"1.3", "1.5-RC1", "1.5", "2.0"})
	if !ok || got.String() != "1.5" {
		t.Errorf("published candidates skip prereleases: %v", got)
	}
}

func TestCVSS3Score(t *testing.T) {
	cases := map[string]float64{
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H": 10.0,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 9.8,
		"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N": 6.5,
		"CVSS:3.0/AV:L/AC:H/PR:H/UI:R/S:C/C:L/I:L/A:N": 3.7,
		"CVSS:3.1/AV:P/AC:H/PR:H/UI:R/S:U/C:N/I:N/A:N": 0,
		"CVSS:3.1/AV:A/AC:L/PR:H/UI:N/S:C/C:L/I:N/A:N": 3.4,
	}
	for vec, want := range cases {
		got, err := CVSS3Score(vec)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v %v, want %v", vec, got, err, want)
		}
	}
	for _, bad := range []string{"CVSS:2.0/AV:N", "CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:X/C:H/I:H/A:H",
		"CVSS:3.1/AV:N/AC:L/PR:X/UI:N/S:U/C:H/I:H/A:H", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/broken"} {
		if _, err := CVSS3Score(bad); err == nil {
			t.Errorf("%s must fail", bad)
		}
	}
	if roundUp(4.02) != 4.1 || roundUp(4.0) != 4.0 {
		t.Error("roundUp")
	}
}

func TestRate(t *testing.T) {
	r := Rate([]deps.Advisory{log4shell()})
	if r.Severity != sdk.SeverityCritical || r.Score != 10 || r.Version != "3" {
		t.Errorf("log4shell: %+v", r)
	}
	v98 := deps.Advisory{Severity: []deps.Score{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}}
	if r := Rate([]deps.Advisory{v98}); r.Severity != sdk.SeverityCritical || r.Score != 9.8 || r.Vector == "" {
		t.Errorf("9.8: %+v", r)
	}
	v4 := deps.Advisory{Severity: []deps.Score{{Type: "CVSS_V4", Score: "CVSS:4.0/AV:N/AC:L", Value: 7.1}}}
	medium := deps.Advisory{Severity: []deps.Score{{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N"}}}
	if r := Rate([]deps.Advisory{medium, v4}); r.Severity != sdk.SeverityHigh || r.Version != "4" {
		t.Errorf("max wins across sources: %+v", r)
	}
	unscoredV4 := deps.Advisory{Severity: []deps.Score{{Type: "CVSS_V4", Score: "CVSS:4.0/AV:N"}}, DatabaseSeverity: "MODERATE"}
	if r := Rate([]deps.Advisory{unscoredV4}); r.Severity != sdk.SeverityMedium {
		t.Errorf("fallback to database severity: %+v", r)
	}
	if r := Rate([]deps.Advisory{{DatabaseSeverity: "LOW"}, {DatabaseSeverity: "HIGH"}, {DatabaseSeverity: "weird"}}); r.Severity != sdk.SeverityHigh {
		t.Errorf("max textual: %+v", r)
	}
	if r := Rate([]deps.Advisory{{}}); r.Severity != sdk.SeverityUnknown {
		t.Errorf("no data: %+v", r)
	}
	same := Rate([]deps.Advisory{{Severity: []deps.Score{{Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N", Value: 7.5}}}, {Severity: []deps.Score{{Score: "x", Value: 8.0}}}})
	if same.Score != 8.0 {
		t.Errorf("higher score at the same level: %+v", same)
	}
}

func TestDeduplicate(t *testing.T) {
	ghsa := deps.Advisory{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Source: "ghsa"}
	other := deps.Advisory{ID: "OSV-2020-1", Source: "osv"}
	chain := deps.Advisory{ID: "CVE-2021-44228", Aliases: []string{"PYSEC-x"}, Source: "osv"}
	groups := Deduplicate([]deps.Advisory{log4shell(), ghsa, other, chain})
	if len(groups) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
	var log *Group
	for i := range groups {
		if len(groups[i].Advisories) == 3 {
			log = &groups[i]
		}
	}
	if log == nil || log.Primary() != "CVE-2021-44228" || strings.Join(log.Aliases(), ",") != "GHSA-jfh8-c2jp-5v3q,PYSEC-x" || strings.Join(log.Sources(), ",") != "ghsa,osv" {
		t.Errorf("merged group: %+v", log)
	}
	g := Group{IDs: []string{"GHSA-1", "OSV-1"}}
	if g.Primary() != "GHSA-1" || (Group{IDs: []string{"OSV-1"}}).Primary() != "OSV-1" {
		t.Error("primary preference")
	}
}
