package osvschema

import (
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

const record = `{"id":"GHSA-x","aliases":["CVE-1"],"summary":"s","details":"d","modified":"2025-01-02T00:00:00Z","published":"2021-12-10T00:00:00Z",
 "severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],
 "affected":[{"package":{"ecosystem":"Maven","name":"g:a"},"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"2.0"}]}],"versions":["1.0"]},
             {"package":{"ecosystem":"PyPI","name":"x"}},{"package":{"ecosystem":"NuGet:Microsoft","name":"N"}}],
 "references":[{"type":"ADVISORY","url":"https://example/advisory"}],"database_specific":{"severity":"HIGH"}}`

func TestToAdvisory(t *testing.T) {
	v, err := Decode([]byte(record))
	if err != nil {
		t.Fatal(err)
	}
	a := v.ToAdvisory("osv")
	if a.ID != "GHSA-x" || a.Source != "osv" || a.DatabaseSeverity != "HIGH" || len(a.Severity) != 1 || len(a.Affected) != 2 ||
		a.Affected[0].Ranges[0].Events[1].Fixed != "2.0" || a.Affected[1].Ecosystem != deps.NuGet || a.References[0].URL == "" || v.IsWithdrawn() {
		t.Errorf("advisory: %+v", a)
	}
	if _, err := Decode([]byte("{")); err == nil {
		t.Error("invalid JSON")
	}
	if _, ok := Ecosystem("PyPI"); ok {
		t.Error("unsupported ecosystem")
	}
	for name, want := range map[string]deps.Ecosystem{"npm": deps.NPM, "Go": deps.Go, "crates.io": deps.CratesIO, "Maven:central": deps.Maven} {
		if got, ok := Ecosystem(name); !ok || got != want {
			t.Errorf("%s: %v %v", name, got, ok)
		}
	}
}
