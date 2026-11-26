package httpcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCache(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	c := &Cache{Dir: t.TempDir(), TTL: time.Hour, Now: func() time.Time { return now }}
	if _, ok := c.Get("k"); ok {
		t.Error("empty cache")
	}
	if err := c.Put("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if b, ok := c.Get("k"); !ok || string(b) != "v" {
		t.Error("hit")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := c.Get("k"); ok {
		t.Error("expired")
	}
	var nilCache *Cache
	if _, ok := nilCache.Get("k"); ok || nilCache.Put("k", nil) != nil {
		t.Error("nil cache")
	}
	disabled := &Cache{Dir: t.TempDir(), Disabled: true}
	_ = disabled.Put("k", []byte("v"))
	if _, ok := disabled.Get("k"); ok {
		t.Error("disabled")
	}
	def := &Cache{Dir: t.TempDir()}
	_ = def.Put("k", []byte("v"))
	if _, ok := def.Get("k"); !ok {
		t.Error("default TTL and clock")
	}
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o600)
	if err := (&Cache{Dir: file}).Put("k", nil); err == nil {
		t.Error("cache dir is a file")
	}
	// Unreadable entry.
	p := def.path("dir")
	_ = os.MkdirAll(p, 0o755)
	if _, ok := def.Get("dir"); ok {
		t.Error("directory entry")
	}
	if err := def.Put("dir", []byte("x")); err == nil {
		t.Error("rename onto a directory")
	}
}
