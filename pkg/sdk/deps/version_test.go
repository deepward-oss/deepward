package deps

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/google/osv-scalibr/semantic"
)

func TestEcosystemMapping(t *testing.T) {
	if Maven.PURLType() != "maven" || NuGet.PURLType() != "nuget" || Ecosystem("PyPI").PURLType() != "pypi" {
		t.Error("PURLType")
	}
	for typ, want := range map[string]Ecosystem{"maven": Maven, "NUGET": NuGet} {
		got, err := EcosystemOf(typ)
		if err != nil || got != want {
			t.Errorf("EcosystemOf(%s) = %v %v", typ, got, err)
		}
	}
	if _, err := EcosystemOf("pypi"); err == nil {
		t.Error("pypi must be unsupported")
	}
}

func TestParseVersionErrors(t *testing.T) {
	if _, err := ParseVersion(Maven, " "); err == nil {
		t.Error("empty")
	}
	if _, err := ParseVersion("PyPI", "1.0.0"); err == nil {
		t.Error("unsupported ecosystem")
	}
	if _, err := ParseVersion(NuGet, "not.a.version.at.all.x"); err == nil {
		// NuGet parsing is lenient in semantic; accept either outcome but
		// make sure the call does not panic.
		t.Log("nuget parser accepted a lenient version")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustParseVersion must panic")
		}
	}()
	MustParseVersion(Maven, "")
}

func TestMavenOrdering(t *testing.T) {
	ordered := []string{"2.0-beta9", "2.14.1", "2.15.0-rc1", "2.15.0", "2.15.1", "2.17.2"}
	for i := 1; i < len(ordered); i++ {
		a, b := MustParseVersion(Maven, ordered[i-1]), MustParseVersion(Maven, ordered[i])
		if !a.Less(b) || b.Compare(a) != 1 {
			t.Errorf("%s must be < %s", a, b)
		}
	}
	if MustParseVersion(Maven, "1.0").Compare(MustParseVersion(Maven, "1.0.0")) != 0 {
		t.Error("1.0 == 1.0.0 in Maven")
	}
}

func TestNuGetOrdering(t *testing.T) {
	a, b := MustParseVersion(NuGet, "3.0.0-preview.1"), MustParseVersion(NuGet, "3.0.0")
	if !a.Less(b) {
		t.Error("prerelease < release")
	}
}

func TestCrossEcosystemCompareIsTotal(t *testing.T) {
	a, b := MustParseVersion(Maven, "1.0"), MustParseVersion(NuGet, "1.0")
	if a.Compare(b) != 0 {
		t.Error("same raw text compares equal across ecosystems")
	}
	if (Version{}).Compare(a) >= 0 || !(Version{}).IsZero() || a.IsZero() {
		t.Error("zero version")
	}
	if a.Ecosystem() != Maven || a.String() != "1.0" {
		t.Error("accessors")
	}
}

func TestIsPrerelease(t *testing.T) {
	cases := []struct {
		eco  Ecosystem
		v    string
		want bool
	}{
		{Maven, "3.0.0-M1", true}, {Maven, "3.0.0-RC2", true}, {Maven, "2.0.0-beta", true},
		{Maven, "1.0-SNAPSHOT", true}, {Maven, "6.0.0.Alpha3", true}, {Maven, "1.0.0-alpha.1", true},
		{Maven, "1.2.3", false}, {Maven, "5.3.0.Final", false}, {Maven, "1.0.0.RELEASE", false},
		{Maven, "2.12.7.1", false}, {Maven, "31.1-jre", false}, {Maven, "1.0-incubating", true},
		{NuGet, "3.0.0-preview.1", true}, {NuGet, "13.0.1", false}, {NuGet, "1.0.0+build.5", false},
	}
	for _, c := range cases {
		if got := MustParseVersion(c.eco, c.v).IsPrerelease(); got != c.want {
			t.Errorf("%s %s prerelease = %v want %v", c.eco, c.v, got, c.want)
		}
	}
}

func TestSegmentsAndClassify(t *testing.T) {
	maj, min, pat, ok := MustParseVersion(Maven, "1.4.2").Segments()
	if !ok || maj != 1 || min != 4 || pat != 2 {
		t.Errorf("segments %d %d %d %v", maj, min, pat, ok)
	}
	if _, _, _, ok := MustParseVersion(Maven, "abc").Segments(); ok {
		t.Error("non-numeric version has no segments")
	}
	cur := MustParseVersion(Maven, "1.4.2")
	cases := map[string]UpdateType{"1.4.5": UpdatePatch, "1.5.0": UpdateMinor, "2.0.1": UpdateMajor, "1.4.2": UpdateNone, "1.3.9": UpdateNone}
	for v, want := range cases {
		if got := ClassifyUpdate(cur, MustParseVersion(Maven, v)); got != want {
			t.Errorf("classify 1.4.2 -> %s = %q want %q", v, got, want)
		}
	}
	if ClassifyUpdate(MustParseVersion(Maven, "abc"), MustParseVersion(Maven, "abd")) != UpdateMajor {
		t.Error("non-numeric versions classify as major")
	}
	if UpdateMajor.Rank() <= UpdateMinor.Rank() || UpdateMinor.Rank() <= UpdatePatch.Rank() || UpdatePatch.Rank() <= UpdateNone.Rank() {
		t.Error("ranks")
	}
}

// TestCompareMatchesSemantic is a property test: our ordering must agree with
// osv-scalibr for random version pairs.
func TestCompareMatchesSemantic(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	quals := []string{"", "-alpha", "-beta2", "-RC1", "-SNAPSHOT", ".Final", "-M3", "-jre"}
	gen := func() string {
		return string(rune('0'+r.Intn(4))) + "." + string(rune('0'+r.Intn(10))) + "." + string(rune('0'+r.Intn(10))) + quals[r.Intn(len(quals))]
	}
	var vs []Version
	for i := 0; i < 300; i++ {
		a, b := gen(), gen()
		va, vb := MustParseVersion(Maven, a), MustParseVersion(Maven, b)
		want, err := semantic.MustParse(a, "Maven").CompareStr(b)
		if err != nil {
			t.Fatal(err)
		}
		if got := va.Compare(vb); got != want {
			t.Fatalf("Compare(%s,%s) = %d, semantic = %d", a, b, got, want)
		}
		vs = append(vs, va)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Less(vs[j]) })
	for i := 1; i < len(vs); i++ {
		if vs[i].Less(vs[i-1]) {
			t.Fatal("sort not consistent")
		}
	}
}
