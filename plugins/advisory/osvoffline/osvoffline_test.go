package osvoffline

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/depstest"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// export builds an all.zip from the recorded OSV records of the osv plugin.
func export(t *testing.T) []byte {
	t.Helper()
	files, _ := filepath.Glob("../osv/testdata/GHSA-*.json")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create(filepath.Base(f))
		_, _ = w.Write(b)
	}
	w, _ := zw.Create("withdrawn.json")
	_, _ = w.Write([]byte(`{"id":"W","withdrawn":"2024-01-01T00:00:00Z"}`))
	w, _ = zw.Create("broken.json")
	_, _ = w.Write([]byte(`{`))
	_, _ = zw.Create("README.md")
	_ = zw.Close()
	return buf.Bytes()
}

// noNetwork fails the test on any HTTP request.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("unexpected network request: %s", r.URL)
	return nil, http.ErrServerClosed
}

func newSource(t *testing.T, dir string, client *http.Client) *Source {
	s := New()
	s.cfg.Dir = dir
	s.cfg.Ecosystems = []string{"Maven"}
	s.HTTP = client
	_ = s.Init(context.Background(), &sdktest.FakeHost{ClockValue: sdk.FixedClock{T: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)}})
	return s
}

func TestUpdateAndOfflineQuery(t *testing.T) {
	zipData := export(t)
	var gets atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		if r.URL.Path != "/Maven/all.zip" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(zipData)
	}))
	defer srv.Close()
	dir := t.TempDir()
	s := newSource(t, dir, srv.Client())
	s.cfg.BaseURL = srv.URL
	if s.Available() {
		t.Error("no database yet")
	}
	msg, err := s.UpdateData(context.Background())
	if err != nil || !strings.Contains(msg, "Maven: downloaded 8 advisories") {
		t.Fatalf("update: %q %v", msg, err)
	}
	msg, _ = s.UpdateData(context.Background())
	if !strings.Contains(msg, "up to date") {
		t.Errorf("second update must use the ETag: %q", msg)
	}
	if !s.Available() || s.Freshness() == nil || s.Freshness().Day() != 25 {
		t.Errorf("availability/freshness: %v", s.Freshness())
	}
	// A fresh source in offline mode must not touch the network.
	off := newSource(t, dir, &http.Client{Transport: noNetwork{t}})
	advs := depstest.TestAdvisorySource(t, off, []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "org.apache.logging.log4j:log4j-core", Version: "2.14.1"}})
	if len(advs) != 8 || advs[0].Source != ID {
		t.Errorf("advisories: %d", len(advs))
	}
	if res, _ := off.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.NuGet, Name: "x"}}); len(res) != 0 {
		t.Error("other ecosystem")
	}
}

func TestFailures(t *testing.T) {
	missing := newSource(t, t.TempDir(), nil)
	if _, err := missing.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "x"}}); err == nil || !strings.Contains(err.Error(), "deepward db update") {
		t.Errorf("missing database: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := missing.Query(ctx, nil); err == nil {
		t.Error("cancelled")
	}
	noDir := New()
	if noDir.Available() {
		t.Error("no dir")
	}
	if _, err := noDir.UpdateData(context.Background()); err == nil {
		t.Error("update without dir")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/bad") {
			_, _ = w.Write([]byte("not a zip"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := newSource(t, t.TempDir(), srv.Client())
	s.cfg.BaseURL = srv.URL
	if _, err := s.UpdateData(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("5xx: %v", err)
	}
	s.cfg.BaseURL = srv.URL + "/bad"
	if _, err := s.UpdateData(context.Background()); err == nil || !strings.Contains(err.Error(), "not a valid export") {
		t.Errorf("invalid zip: %v", err)
	}
	s.cfg.BaseURL = "http://127.0.0.1:1"
	if _, err := s.UpdateData(context.Background()); err == nil {
		t.Error("unreachable")
	}
	s.cfg.BaseURL = "http://[::1"
	if _, err := s.UpdateData(context.Background()); err == nil {
		t.Error("bad url")
	}
	if s.DataLicense().SPDX != "CC-BY-4.0" || s.ConfigTarget() == nil {
		t.Error("metadata")
	}
	other := newSource(t, t.TempDir(), nil)
	other.cfg.Ecosystems = []string{"PyPI"}
	_ = os.MkdirAll(filepath.Join(other.dir, "PyPI"), 0o755)
	_ = os.WriteFile(other.zipPath("PyPI"), export(t), 0o644)
	if res, err := other.Query(context.Background(), []deps.PackageQuery{{Ecosystem: deps.Maven, Name: "x"}}); err != nil || len(res) != 0 {
		t.Errorf("unsupported ecosystem skipped: %v", err)
	}
}
