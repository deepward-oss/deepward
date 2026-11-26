package nuget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
)

// NuGetOrg is the public service index.
const NuGetOrg = "https://api.nuget.org/v3/index.json"

// feed is a NuGet v3 source.
type feed struct {
	ID       string
	Index    string
	Username string
	Password string
	Token    string
}

// feedClient lists versions through the flat container (PackageBaseAddress).
type feedClient struct {
	feeds   []feed
	http    *http.Client
	cache   *httpcache.Cache
	retries int
	backoff time.Duration
	mu      sync.Mutex
	bases   map[string]string
}

func (c *feedClient) base(ctx context.Context, f feed) (string, error) {
	c.mu.Lock()
	b, ok := c.bases[f.Index]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	data, status, err := c.get(ctx, f, f.Index)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("feed %s: service index HTTP %d", f.ID, status)
	}
	var idx struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", fmt.Errorf("feed %s: invalid service index: %w", f.ID, err)
	}
	for _, r := range idx.Resources {
		if strings.HasPrefix(r.Type, "PackageBaseAddress/3.0.0") {
			b = strings.TrimSuffix(r.ID, "/") + "/"
			c.mu.Lock()
			c.bases[f.Index] = b
			c.mu.Unlock()
			return b, nil
		}
	}
	return "", fmt.Errorf("feed %s: no PackageBaseAddress/3.0.0 resource", f.ID)
}

// Versions merges the version lists of all feeds.
func (c *feedClient) Versions(ctx context.Context, id string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	var errs []error
	for _, f := range c.feeds {
		b, err := c.base(ctx, f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		data, status, err := c.get(ctx, f, b+strings.ToLower(id)+"/index.json")
		switch {
		case err != nil:
			errs = append(errs, err)
			continue
		case status == http.StatusNotFound:
			continue
		case status != http.StatusOK:
			errs = append(errs, fmt.Errorf("feed %s: HTTP %d", f.ID, status))
			continue
		}
		var idx struct {
			Versions []string `json:"versions"`
		}
		if err := json.Unmarshal(data, &idx); err != nil {
			errs = append(errs, fmt.Errorf("feed %s: invalid version index: %w", f.ID, err))
			continue
		}
		for _, v := range idx.Versions {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	if len(out) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, deps.ErrPackageNotFound
	}
	sort.Strings(out)
	return out, nil
}

// get fetches a URL with caching (200 only) and retries on 5xx.
func (c *feedClient) get(ctx context.Context, f feed, url string) ([]byte, int, error) {
	if b, ok := c.cache.Get(url); ok {
		return b, http.StatusOK, nil
	}
	retries, backoff := c.retries, c.backoff
	if retries <= 0 {
		retries = 3
	}
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", "deepward")
		switch {
		case f.Token != "":
			req.Header.Set("Authorization", "Bearer "+f.Token)
		case f.Username != "":
			req.SetBasicAuth(f.Username, f.Password)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			last = fmt.Errorf("feed %s: %w", f.ID, err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode >= 500 {
			last = fmt.Errorf("feed %s: HTTP %d", f.ID, resp.StatusCode)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			_ = c.cache.Put(url, data)
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, last
}
