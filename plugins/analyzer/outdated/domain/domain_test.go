package domain

import (
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

func mv(s string) deps.Version { return deps.MustParseVersion(deps.Maven, s) }

func TestClassify(t *testing.T) {
	u := Classify(mv("1.4.2"), []string{"1.4.0", "1.4.5", "1.4.3", "1.5.0", "1.6.1", "2.0.1", "2.1.0-M1", "garbage!"}, Policy{})
	if u.Patch != "1.4.5" || u.Minor != "1.6.1" || u.Major != "2.0.1" || u.Latest != "2.0.1" || u.Type != deps.UpdateMajor {
		t.Errorf("updates: %+v", u)
	}
	if u := Classify(mv("2.0.1"), []string{"1.0", "2.0.1"}, Policy{}); u.Any() || u.Type != deps.UpdateNone {
		t.Errorf("up to date: %+v", u)
	}
	if u := Classify(mv("1.4.2"), []string{"1.4.3"}, Policy{}); u.Type != deps.UpdatePatch {
		t.Errorf("patch only: %+v", u)
	}
	if u := Classify(mv("1.4.2"), []string{"1.5.0"}, Policy{}); u.Type != deps.UpdateMinor {
		t.Errorf("minor only: %+v", u)
	}
}

func TestPrereleases(t *testing.T) {
	published := []string{"3.0.0-M1", "3.0.0-RC2", "2.7.18"}
	if u := Classify(mv("2.7.0"), published, Policy{}); u.Latest != "2.7.18" {
		t.Errorf("stable current must not get prereleases: %+v", u)
	}
	if u := Classify(mv("2.7.0"), published, Policy{AllowPrerelease: true}); u.Latest != "3.0.0-RC2" {
		t.Errorf("allowed prereleases: %+v", u)
	}
	nuget := Classify(deps.MustParseVersion(deps.NuGet, "2.0.0"), []string{"3.0.0-preview.1"}, Policy{})
	if nuget.Any() {
		t.Error("NuGet preview excluded")
	}
	rc := Classify(mv("2.0.0-RC1"), []string{"2.0.0-RC2", "2.1.0-RC1", "2.0.0", "2.0.1"}, Policy{})
	if rc.Latest != "2.0.1" || rc.Patch != "2.0.1" {
		t.Errorf("prerelease current: %+v", rc)
	}
	onlyRC := Classify(mv("2.0.0-RC1"), []string{"2.0.0-RC2", "2.1.0-RC1"}, Policy{})
	if onlyRC.Latest != "2.0.0-RC2" {
		t.Errorf("same-line prerelease: %+v", onlyRC)
	}
}

func TestRules(t *testing.T) {
	rules := []Rule{
		{Match: "pkg:maven/org.springframework*/*", MaxUpdate: deps.UpdateMinor},
		{Match: "pkg:maven/org.springframework.boot/*", MaxUpdate: deps.UpdatePatch},
		{Match: "pkg:nuget/newtonsoft.json", Ignore: true},
		{Match: "pkg:maven/com.google.guava/guava", Allow: ">=31, <33", AllowPrerelease: true},
	}
	p, _ := PolicyFor(rules, "pkg:maven/org.springframework/spring-web")
	if p.MaxUpdate != deps.UpdateMinor {
		t.Errorf("spring: %+v", p)
	}
	u := Classify(mv("5.3.0"), []string{"5.3.39", "6.0.0", "6.1.2"}, p)
	if u.Major != "" || u.Latest != "5.3.39" {
		t.Errorf("no major for Spring: %+v", u)
	}
	p, _ = PolicyFor(rules, "pkg:maven/org.springframework.boot/spring-boot")
	if p.MaxUpdate != deps.UpdatePatch {
		t.Errorf("strictest cap wins: %+v", p)
	}
	p, _ = PolicyFor(rules, "pkg:nuget/Newtonsoft.Json")
	if !p.Ignore {
		t.Error("ignore (case-insensitive)")
	}
	p, _ = PolicyFor(rules, "pkg:maven/com.google.guava/guava")
	u = Classify(mv("31.0-jre"), []string{"31.1-jre", "32.1.3-jre", "33.0.0-jre", "bad!"}, p)
	if u.Latest != "32.1.3-jre" || !p.AllowPrerelease {
		t.Errorf("allow range: %+v", u)
	}
	if (Rule{}).Matches("x") {
		t.Error("empty rule")
	}
	if _, err := PolicyFor([]Rule{{Match: "*", Allow: "~1"}}, "x"); err == nil {
		t.Error("bad range")
	}
	rs, err := ParseRanges("=1.0, !=1.1, >0.9, <=2")
	if err != nil || len(rs) != 4 {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{"1.0": true, "1.1": false} {
		if allowed(rs, mv(v)) != want {
			t.Errorf("allowed %s", v)
		}
	}
	bad := []Range{{Op: ">=", V: ""}}
	if allowed(bad, mv("1.0")) {
		t.Error("unparseable bound")
	}
}
