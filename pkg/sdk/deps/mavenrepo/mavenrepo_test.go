package mavenrepo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
)

const meta = `<metadata><versioning><latest>2.0</latest><versions><version>1.0</version><version> 2.0 </version><version></version></versions></versioning></metadata>`

func TestVersionsAcrossRepositories(t *testing.T) {
	var calls atomic.Int64
	var flaky atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/down/"):
			w.WriteHeader(http.StatusBadGateway)
		case strings.HasPrefix(r.URL.Path, "/flaky/"):
			if flaky.Add(1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`<metadata><versioning><versions><version>3.0</version></versions></versioning></metadata>`))
		case strings.HasPrefix(r.URL.Path, "/auth/"):
			if r.Header.Get("Authorization") != "Bearer t0k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(meta))
		case r.URL.Path == "/central/org/x/lib/maven-metadata.xml":
			_, _ = w.Write([]byte(meta))
		case r.URL.Path == "/bad/org/x/lib/maven-metadata.xml":
			_, _ = w.Write([]byte("<metadata"))
		case r.URL.Path == "/central/org/x/lib/1.0/lib-1.0.pom":
			_, _ = w.Write([]byte("<project/>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Repositories: []Repository{{ID: "central", URL: srv.URL + "/central/"}, {ID: "flaky", URL: srv.URL + "/flaky"}}, Backoff: time.Millisecond, UserAgent: "deepward-test"}
	vs, err := c.Versions(context.Background(), "org.x", "lib")
	if err != nil || strings.Join(vs, ",") != "1.0,2.0,3.0" {
		t.Errorf("merged: %v %v", vs, err)
	}
	down := &Client{Repositories: []Repository{{ID: "down", URL: srv.URL + "/down"}}, Backoff: time.Millisecond}
	before := calls.Load()
	_, err = down.Versions(context.Background(), "org.x", "lib")
	var re *RepoError
	if !errors.As(err, &re) || re.Status != http.StatusBadGateway || calls.Load()-before != 4 || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("5xx after 3 retries: %v (%d calls)", err, calls.Load()-before)
	}
	if _, err := (&Client{Repositories: []Repository{{ID: "c", URL: srv.URL + "/central"}}}).Versions(context.Background(), "org.y", "none"); !errors.Is(err, deps.ErrPackageNotFound) {
		t.Errorf("not found: %v", err)
	}
	auth := &Client{Repositories: []Repository{{ID: "a", URL: srv.URL + "/auth", Token: "t0k"}}}
	if vs, err := auth.Versions(context.Background(), "org.x", "lib"); err != nil || len(vs) != 2 {
		t.Errorf("token auth: %v %v", vs, err)
	}
	noAuth := &Client{Repositories: []Repository{{ID: "a", URL: srv.URL + "/auth", Username: "u", Password: "p"}}}
	if _, err := noAuth.Versions(context.Background(), "org.x", "lib"); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("unauthorized: %v", err)
	}
	bad := &Client{Repositories: []Repository{{ID: "b", URL: srv.URL + "/bad"}}}
	if _, err := bad.Versions(context.Background(), "org.x", "lib"); err == nil || !strings.Contains(err.Error(), "invalid maven-metadata.xml") {
		t.Errorf("bad metadata: %v", err)
	}
	pom, err := c.FetchPOM(context.Background(), "org.x", "lib", "1.0")
	if err != nil || string(pom) != "<project/>" {
		t.Errorf("pom: %s %v", pom, err)
	}
	centralOnly := &Client{Repositories: []Repository{{ID: "central", URL: srv.URL + "/central"}}}
	if _, err := centralOnly.FetchPOM(context.Background(), "org.x", "lib", "9"); err == nil {
		t.Error("missing pom")
	}
	if _, err := (&Client{Repositories: []Repository{}}).FetchPOM(context.Background(), "g", "a", "1"); err == nil {
		t.Error("default central unreachable or missing")
	}
}

func TestCacheAndNetworkErrors(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(meta))
	}))
	cache := &httpcache.Cache{Dir: t.TempDir()}
	c := &Client{Repositories: []Repository{{ID: "c", URL: srv.URL}}, Cache: cache}
	_, _ = c.Versions(context.Background(), "g", "a")
	_, _ = c.Versions(context.Background(), "g", "a")
	if calls.Load() != 1 {
		t.Errorf("cache: %d calls", calls.Load())
	}
	srv.Close()
	net := &Client{Repositories: []Repository{{ID: "dead", URL: srv.URL}}, Backoff: time.Millisecond, Retries: 1}
	if _, err := net.Versions(context.Background(), "g", "b"); err == nil || !strings.Contains(err.Error(), "repository dead") {
		t.Errorf("network: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := net.Versions(ctx, "g", "b"); err == nil {
		t.Error("cancelled")
	}
	badURL := &Client{Repositories: []Repository{{ID: "x", URL: "http://[::1"}}, Retries: 1, Backoff: time.Millisecond}
	if _, err := badURL.Versions(context.Background(), "g", "b"); err == nil {
		t.Error("bad url")
	}
	if (&RepoError{Repo: "r", Err: errors.New("x")}).Unwrap() == nil {
		t.Error("unwrap")
	}
	if vs, err := ParseMetadata([]byte(meta)); err != nil || len(vs) != 3 {
		t.Errorf("parse: %v", vs)
	}
}

func TestCancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c := &Client{Repositories: []Repository{{ID: "c", URL: srv.URL}}, Backoff: time.Second}
	if _, err := c.Versions(ctx, "g", "a"); err == nil {
		t.Error("expected cancellation during backoff")
	}
}
