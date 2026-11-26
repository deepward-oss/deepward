// Package download fetches a tool archive from a configured URL, verifies
// its SHA-256 checksum and caches it.
package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// MaxSize bounds downloaded archives (1 GiB).
const MaxSize = 1 << 30

// Resolver is the download tool resolver.
type Resolver struct {
	Client *http.Client
}

func init() { sdk.Register(&Resolver{}) }

// Info implements sdk.Plugin.
func (*Resolver) Info() sdk.Info {
	return sdk.Info{ID: "download", Kind: sdk.KindToolResolver, Version: sdk.BuiltinVersion,
		Description: "Downloads tools.<name>.download.url, verifies its SHA-256 and caches it (opt-in)"}
}

// Resolve implements sdk.ToolResolver.
func (r *Resolver) Resolve(ctx context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	d := req.Settings.Download
	if d == nil || d.URL == "" {
		return nil, nil
	}
	want := strings.ToLower(strings.TrimSpace(d.SHA256))
	if want == "" {
		return nil, errors.New("download refused: sha256 is not configured")
	}
	if req.CacheDir == "" {
		return nil, errors.New("no cache directory for downloads")
	}
	u, err := url.Parse(d.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid download URL: %w", err)
	}
	dir := filepath.Join(req.CacheDir, want)
	name := path.Base(u.Path)
	binary := d.Binary
	if binary == "" {
		binary = name
	}
	target := filepath.Join(dir, filepath.FromSlash(binary))
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		return &sdk.ToolCandidate{Command: []string{target}, Source: d.URL + " (cached)"}, nil
	}
	tmp, err := r.fetch(ctx, d.URL, want)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		err = unzip(tmp, dir)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		err = untar(tmp, dir)
	default:
		err = copyFile(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("download: %s not found in %s", binary, name)
	}
	_ = os.Chmod(target, 0o755)
	return &sdk.ToolCandidate{Command: []string{target}, Source: d.URL}, nil
}

func (r *Resolver) fetch(ctx context.Context, rawURL, want string) (string, error) {
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	f, err := os.CreateTemp("", "deepward-download-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, MaxSize+1))
	_ = f.Close()
	if err == nil && n > MaxSize {
		err = errors.New("download exceeds the 1 GiB limit")
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("checksum mismatch for %s: got sha256 %s, expected %s", rawURL, got, want)
	}
	return f.Name(), nil
}

// safeJoin rejects archive entries that escape dir.
func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the target directory", name)
	}
	return p, nil
}

func unzip(src, dir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(p, rc, f.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untar(src, dir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open tar.gz: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		p, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(p, tr, os.FileMode(h.Mode)); err != nil {
				return err
			}
		}
	}
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(r, MaxSize)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, in, 0o755)
}
