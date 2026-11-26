package osv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

type replay struct {
	batchCalls atomic.Int64
	vulnCalls  atomic.Int64
	failFirst  atomic.Int64
	paginate   bool
}

func (r *replay) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r.failFirst.Load() > 0 {
			r.failFirst.Add(-1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch {
		case req.URL.Path == "/v1/querybatch":
			r.batchCalls.Add(1)
			body, _ := io.ReadAll(req.Body)
			var q struct {
				Queries []struct {
					PageToken string `json:"page_token"`
				} `json:"queries"`
			}
			_ = json.Unmarshal(body, &q)
			if r.paginate && q.Queries[0].PageToken == "" {
				_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"GHSA-jfh8-c2jp-5v3q","modified":"2025-01-01T00:00:00Z"}],"next_page_token":"p2"},{}]}`))
				return
			}
			if r.paginate {
				_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"GHSA-7rjr-3q55-vv33","modified":"2025-01-01T00:00:00Z"}]}]}`))
				return
			}
			data, err := os.ReadFile("testdata/querybatch.json")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(data)
		case strings.HasPrefix(req.URL.Path, "/v1/vulns/"):
			r.vulnCalls.Add(1)
			data, err := os.ReadFile("testdata/" + strings.TrimPrefix(req.URL.Path, "/v1/vulns/") + ".json")
			if err != nil {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, req)
		}
	}
}

func newSource(t *testing.T, url string) *Source {
	s := New()
	s.cfg.APIURL = url
	if err := s.Init(context.Background(), &sdktest.FakeHost{Run: sdk.RunSettings{CacheDir: t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	return s
}

var queries = []deps.PackageQuery{
	{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1"},
	{Ecosystem: deps.Maven, Name: "org.yaml:snakeyaml", Version: "2.2"},
}

func TestRecordedResponses(t *testing.T) {
	r := &replay{}
	srv := httptest.NewServer(r.handler(t))
	defer srv.Close()
	s := newSource(t, srv.URL)
	advs := depstest.TestAdvisorySource(t, s, queries)
	if len(advs) != 8 {
		t.Fatalf("advisories: %d", len(advs))
	}
	var log4shell deps.Advisory
	for _, a := range advs {
		if a.ID == "GHSA-jfh8-c2jp-5v3q" {
			log4shell = a
		}
	}
	if log4shell.Aliases[0] != "CVE-2021-44228" || log4shell.DatabaseSeverity != "CRITICAL" || log4shell.Source != "osv" ||
		len(log4shell.Affected) == 0 || log4shell.Affected[0].Ecosystem != deps.Maven || len(log4shell.References) == 0 {
		t.Errorf("log4shell: %+v", log4shell)
	}
	vulnCalls := r.vulnCalls.Load()
	_, _ = s.Query(context.Background(), queries)
	if r.vulnCalls.Load() != vulnCalls {
		t.Error("details must be cached by id and modified time")
	}
	if !s.Used() || s.Freshness() != nil || s.DataLicense().SPDX != "CC-BY-4.0" {
		t.Error("metadata")
	}
}

func TestPaginationRetriesAndErrors(t *testing.T) {
	r := &replay{paginate: true}
	r.failFirst.Store(1)
	srv := httptest.NewServer(r.handler(t))
	defer srv.Close()
	s := newSource(t, srv.URL)
	s.cfg.BatchSize = 1
	advs, err := s.Query(context.Background(), queries[:1])
	if err != nil || len(advs) != 2 {
		t.Fatalf("pagination: %v %v", advs, err)
	}
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"GHSA-missing"}]}]}`))
			return
		}
		http.NotFound(w, req)
	}))
	defer missing.Close()
	if _, err := newSource(t, missing.URL).Query(context.Background(), queries[:1]); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("missing details: %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"X"}]}]}`))
			return
		}
		_, _ = w.Write([]byte("not json"))
	}))
	defer bad.Close()
	if _, err := newSource(t, bad.URL).Query(context.Background(), queries[:1]); err == nil || !strings.Contains(err.Error(), "decode X") {
		t.Errorf("bad details: %v", err)
	}
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer garbage.Close()
	if _, err := newSource(t, garbage.URL).Query(context.Background(), queries[:1]); err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Errorf("garbage batch: %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	if _, err := newSource(t, down.URL).Query(context.Background(), queries[:1]); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("5xx: %v", err)
	}
	withdrawn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"W"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"W","withdrawn":"2024-01-01T00:00:00Z"}`))
	}))
	defer withdrawn.Close()
	if advs, err := newSource(t, withdrawn.URL).Query(context.Background(), queries[:1]); err != nil || len(advs) != 0 {
		t.Errorf("withdrawn: %v %v", advs, err)
	}
	dead := New()
	dead.cfg.APIURL = "http://127.0.0.1:1"
	if _, err := dead.Query(context.Background(), queries[:1]); err == nil {
		t.Error("unreachable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dead.Query(ctx, queries[:1]); err == nil {
		t.Error("cancelled")
	}
	badURL := New()
	badURL.cfg.APIURL = "http://[::1"
	if _, err := badURL.Query(context.Background(), queries[:1]); err == nil {
		t.Error("bad url")
	}
	if err := (&Config{BatchSize: 5000}).Validate(); err == nil {
		t.Error("batch size")
	}
	if err := (&Config{Concurrency: -1}).Validate(); err == nil {
		t.Error("concurrency")
	}
}
