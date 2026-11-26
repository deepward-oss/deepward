package ghsa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/plugins/analyzer/depvuln/domain"
)

func server(t *testing.T) (*httptest.Server, *[]*http.Request) {
	var reqs []*http.Request
	recorded, err := os.ReadFile("testdata/log4j.json")
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r)
		switch {
		case r.URL.Query().Get("page") == "2":
			_, _ = w.Write([]byte(`[{"ghsa_id":"GHSA-page-2","withdrawn_at":"2024-01-01T00:00:00Z"}]`))
		case strings.Contains(r.URL.Query().Get("affects"), "log4j-core"):
			w.Header().Set("Link", `<`+srv.URL+`/advisories?page=2>; rel="next"`)
			_, _ = w.Write(recorded)
		case strings.Contains(r.URL.Query().Get("affects"), "boom"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		case strings.Contains(r.URL.Query().Get("affects"), "garbage"):
			_, _ = w.Write([]byte(`{`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	return srv, &reqs
}

func TestRecordedAPI(t *testing.T) {
	srv, reqs := server(t)
	defer srv.Close()
	s := New()
	s.cfg.APIURL = srv.URL
	s.Getenv = func(k string) string { return map[string]string{"GITHUB_TOKEN": "ghp_test"}[k] }
	q := []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1"}, {Ecosystem: deps.NuGet, Name: "Newtonsoft.Json", Version: "12.0.3"}}
	advs := depstest.TestAdvisorySource(t, s, q)
	var log4shell deps.Advisory
	for _, a := range advs {
		if a.ID == "GHSA-jfh8-c2jp-5v3q" {
			log4shell = a
		}
	}
	if log4shell.ID == "" || log4shell.Aliases[0] != "CVE-2021-44228" || log4shell.DatabaseSeverity != "critical" || log4shell.Severity[0].Value != 10 {
		t.Fatalf("log4shell: %+v", log4shell)
	}
	if !domain.Affects(log4shell, deps.Maven, "org.apache.logging.log4j:log4j-core", deps.MustParseVersion(deps.Maven, "2.14.1")) ||
		domain.Affects(log4shell, deps.Maven, "org.apache.logging.log4j:log4j-core", deps.MustParseVersion(deps.Maven, "2.15.0")) {
		t.Error("ranges converted from vulnerable_version_range")
	}
	first := (*reqs)[0]
	if first.Header.Get("Authorization") != "Bearer ghp_test" || first.URL.Query().Get("ecosystem") != "maven" {
		t.Errorf("request: %v %v", first.Header, first.URL)
	}
	// Deduplication with OSV in the analyzer domain.
	osvCopy := log4shell
	osvCopy.ID, osvCopy.Aliases, osvCopy.Source = "CVE-2021-44228", []string{"GHSA-jfh8-c2jp-5v3q"}, "osv"
	if g := domain.Deduplicate([]deps.Advisory{log4shell, osvCopy}); len(g) != 1 || len(g[0].Sources()) != 2 {
		t.Errorf("dedup with OSV: %+v", g)
	}
}

func TestErrorsAndConversion(t *testing.T) {
	srv, _ := server(t)
	defer srv.Close()
	s := New()
	s.cfg.APIURL = srv.URL
	s.Getenv = func(string) string { return "" }
	for name, want := range map[string]string{"boom": "HTTP 403", "garbage": "invalid response"} {
		if _, err := s.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.Maven, Name: name, Version: "1"}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	dead := New()
	dead.cfg.APIURL = "http://127.0.0.1:1"
	if _, err := dead.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "x", Version: "1"}}); err == nil {
		t.Error("unreachable")
	}
	bad := New()
	bad.cfg.APIURL = "http://[::1"
	if _, err := bad.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "x", Version: "1"}}); err == nil {
		t.Error("bad url")
	}
	events, exact := RangeToEvents("< 1.33")
	if exact != "" || events[0].Introduced != "0" || events[1].Fixed != "1.33" {
		t.Errorf("upper bound only: %+v", events)
	}
	events, _ = RangeToEvents("> 1.0, <= 2.0")
	if events[0].Introduced != "1.0" || events[1].LastAffected != "2.0" {
		t.Errorf("exclusive/inclusive: %+v", events)
	}
	if _, exact := RangeToEvents("= 6.3.2.1"); exact != "6.3.2.1" {
		t.Error("exact version")
	}
	a := convert(apiAdvisory{GHSAID: "GHSA-x", CVEID: "CVE-1", Vulnerabilities: []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		FirstPatchedVersion    string `json:"first_patched_version"`
	}{{VulnerableVersionRange: "= 1.0"}}})
	if len(a.Aliases) != 1 || len(a.Affected) != 0 {
		t.Errorf("convert: %+v", a)
	}
	if (&Config{ChunkSize: 500}).Validate() == nil || (&Config{}).Validate() != nil || New().ConfigTarget() == nil || New().Freshness() != nil {
		t.Error("config/metadata")
	}
	if res, _ := New().Query(context.Background(), nil); res != nil {
		t.Error("empty query")
	}
}

func TestDisabledByDefault(t *testing.T) {
	reg := sdk.NewRegistry()
	_ = reg.Register(New())
	e, err := engine.New(context.Background(), engine.Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Enabled(deps.KindAdvisorySource)) != 0 || !New().Info().DisabledByDefault {
		t.Error("ghsa must be disabled unless enabled explicitly")
	}
}
