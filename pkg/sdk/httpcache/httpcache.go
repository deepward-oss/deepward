// Package httpcache is a small on-disk cache with a TTL for registry
// metadata responses, shared by plugins that query package registries.
package httpcache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// DefaultTTL is used when no TTL is configured.
const DefaultTTL = 6 * time.Hour

// Cache stores byte payloads keyed by an arbitrary string (usually a URL).
// The zero value and a nil *Cache are disabled caches.
type Cache struct {
	Dir      string
	TTL      time.Duration
	Disabled bool
	// Now overrides the clock (for tests).
	Now func() time.Time
}

func (c *Cache) enabled() bool { return c != nil && !c.Disabled && c.Dir != "" }

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(c.Dir, h[:2], h)
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns a fresh cached payload.
func (c *Cache) Get(key string) ([]byte, bool) {
	if !c.enabled() {
		return nil, false
	}
	p := c.path(key)
	fi, err := os.Stat(p)
	if err != nil {
		return nil, false
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if c.now().Sub(fi.ModTime()) > ttl {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}

// Put stores a payload; errors are ignored by callers on purpose (the cache
// is an optimization), but returned for tests.
func (c *Cache) Put(key string, data []byte) error {
	if !c.enabled() {
		return nil
	}
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	now := c.now()
	return os.Chtimes(p, now, now)
}
