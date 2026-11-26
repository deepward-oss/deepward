package deps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestHTTPGetter(t *testing.T) {
	var calls, failures atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/ok":
			if r.Header.Get("X-Test") != "1" || r.Header.Get("User-Agent") != "deepward" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte("data"))
		case "/flaky":
			if failures.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("recovered"))
		case "/down":
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := NewHTTPGetter(&sdktest.FakeHost{Run: sdk.RunSettings{CacheDir: t.TempDir(), CacheTTL: time.Hour}}, nil, 0)
	g.Backoff = time.Millisecond
	h := http.Header{"X-Test": {"1"}}
	if b, status, err := g.Get(context.Background(), srv.URL+"/ok", h); err != nil || status != 200 || string(b) != "data" {
		t.Fatalf("ok: %s %d %v", b, status, err)
	}
	before := calls.Load()
	if b, _, _ := g.Get(context.Background(), srv.URL+"/ok", h); string(b) != "data" || calls.Load() != before {
		t.Error("second request must come from the cache")
	}
	if b, _, err := g.Get(context.Background(), srv.URL+"/flaky", nil); err != nil || string(b) != "recovered" {
		t.Errorf("retry: %s %v", b, err)
	}
	if _, status, err := g.Get(context.Background(), srv.URL+"/missing", nil); err != nil || status != 404 {
		t.Errorf("404: %d %v", status, err)
	}
	g.Retries = 1
	if _, _, err := g.Get(context.Background(), srv.URL+"/down", nil); err == nil {
		t.Error("5xx after retries")
	}
	if _, _, err := g.Get(context.Background(), "http://127.0.0.1:1/unreachable", nil); err == nil {
		t.Error("network error")
	}
	if _, _, err := g.Get(context.Background(), "::bad", nil); err == nil {
		t.Error("bad url")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := g.Get(ctx, srv.URL+"/down", nil); err == nil {
		t.Error("cancelled")
	}
	g2 := NewHTTPGetter(&sdktest.FakeHost{Run: sdk.RunSettings{NoCache: true}}, &http.Client{}, time.Minute)
	if g2.Cache.TTL != time.Minute || !g2.Cache.Disabled {
		t.Errorf("settings: %+v", g2.Cache)
	}
}
