package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func zipOf(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("dir/")
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func tgzOf(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestDownloadFormatsAndCache(t *testing.T) {
	payloads := map[string][]byte{
		"/semgrep.zip":   zipOf(map[string]string{"semgrep/bin/semgrep": "#!/bin/sh"}),
		"/tool.tar.gz":   tgzOf(map[string]string{"bin/tool": "#!/bin/sh"}),
		"/bare-tool":     []byte("#!/bin/sh"),
		"/evil.zip":      zipOf(map[string]string{"../escape": "x"}),
		"/evil.tgz":      tgzOf(map[string]string{"../../escape": "x"}),
		"/broken.zip":    []byte("not a zip"),
		"/broken.tar.gz": []byte("not gzip"),
		"/wrongname.zip": zipOf(map[string]string{"other": "x"}),
	}
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, ok := payloads[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	cache := t.TempDir()
	r := &Resolver{Client: srv.Client()}
	req := func(path, binary string) sdk.ResolveRequest {
		return sdk.ResolveRequest{CacheDir: cache, Settings: sdk.ToolSettings{Download: &sdk.DownloadSettings{
			URL: srv.URL + path, SHA256: sum(payloads[path]), Binary: binary}}}
	}
	c := sdktest.TestToolResolver(t, r, req("/semgrep.zip", "semgrep/bin/semgrep"))
	if c == nil || !strings.HasSuffix(filepath.ToSlash(c.Command[0]), "semgrep/bin/semgrep") {
		t.Fatalf("zip: %+v", c)
	}
	before := hits.Load()
	c, err := r.Resolve(context.Background(), req("/semgrep.zip", "semgrep/bin/semgrep"))
	if err != nil || hits.Load() != before || !strings.HasSuffix(c.Source, "(cached)") {
		t.Errorf("second resolve must use the cache: %v hits=%d", err, hits.Load()-before)
	}
	if c, err := r.Resolve(context.Background(), req("/tool.tar.gz", "bin/tool")); err != nil || c == nil {
		t.Errorf("tar.gz: %v", err)
	}
	if c, err := r.Resolve(context.Background(), req("/bare-tool", "")); err != nil || filepath.Base(c.Command[0]) != "bare-tool" {
		t.Errorf("bare: %v", err)
	}
	for _, bad := range []struct{ path, binary, want string }{
		{"/evil.zip", "x", "escapes"}, {"/evil.tgz", "x", "escapes"}, {"/broken.zip", "x", "open zip"},
		{"/broken.tar.gz", "x", "open tar.gz"}, {"/wrongname.zip", "tool", "not found in"}, {"/missing", "x", "HTTP 404"},
	} {
		_, err := r.Resolve(context.Background(), req(bad.path, bad.binary))
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s: err = %v, want %q", bad.path, err, bad.want)
		}
	}
	mismatch := req("/bare-tool", "")
	mismatch.CacheDir = t.TempDir()
	mismatch.Settings.Download.SHA256 = strings.Repeat("0", 64)
	if _, err := r.Resolve(context.Background(), mismatch); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("checksum: %v", err)
	}
}

func TestDownloadGuards(t *testing.T) {
	r := &Resolver{}
	if c, err := r.Resolve(context.Background(), sdk.ResolveRequest{}); c != nil || err != nil {
		t.Error("not configured")
	}
	noSum := sdk.ResolveRequest{CacheDir: t.TempDir(), Settings: sdk.ToolSettings{Download: &sdk.DownloadSettings{URL: "https://example.invalid/x"}}}
	if _, err := r.Resolve(context.Background(), noSum); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("missing checksum: %v", err)
	}
	noCache := sdk.ResolveRequest{Settings: sdk.ToolSettings{Download: &sdk.DownloadSettings{URL: "https://x/y", SHA256: "ab"}}}
	if _, err := r.Resolve(context.Background(), noCache); err == nil {
		t.Error("no cache dir")
	}
	badURL := sdk.ResolveRequest{CacheDir: t.TempDir(), Settings: sdk.ToolSettings{Download: &sdk.DownloadSettings{URL: "http://[::1", SHA256: "ab"}}}
	if _, err := r.Resolve(context.Background(), badURL); err == nil {
		t.Error("bad url")
	}
	unreachable := sdk.ResolveRequest{CacheDir: t.TempDir(), Settings: sdk.ToolSettings{Download: &sdk.DownloadSettings{URL: "http://127.0.0.1:1/x", SHA256: "ab"}}}
	if _, err := r.Resolve(context.Background(), unreachable); err == nil {
		t.Error("unreachable")
	}
	if _, err := safeJoin("/a", "../b"); err == nil {
		t.Error("safeJoin")
	}
	if err := copyFile(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Error("copy missing")
	}
	if err := untar(filepath.Join(t.TempDir(), "missing"), t.TempDir()); err == nil {
		t.Error("untar missing")
	}
}
