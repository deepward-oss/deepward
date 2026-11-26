package deps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
)

// HTTPGetter fetches registry metadata for version registries: successful
// responses are cached, server errors and network failures are retried with
// exponential backoff.
type HTTPGetter struct {
	Client  *http.Client
	Cache   *httpcache.Cache
	Retries int
	Backoff time.Duration
}

// NewHTTPGetter builds a getter from the host settings. ttl overrides the
// configured cache TTL when non-zero; client may be nil.
func NewHTTPGetter(h sdk.Host, client *http.Client, ttl time.Duration) *HTTPGetter {
	st := h.Settings()
	if ttl == 0 {
		ttl = st.CacheTTL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPGetter{Client: client, Cache: &httpcache.Cache{Dir: st.CacheDir, TTL: ttl, Disabled: st.NoCache || st.CacheDir == ""}}
}

// Get fetches url and returns the body and status code. Only 200 responses
// are cached.
func (g *HTTPGetter) Get(ctx context.Context, url string, header http.Header) ([]byte, int, error) {
	if b, ok := g.Cache.Get(url); ok {
		return b, http.StatusOK, nil
	}
	retries, backoff := g.Retries, g.Backoff
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
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("User-Agent", "deepward")
		resp, err := g.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			last = err
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode >= 500 {
			last = fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL.Host)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			_ = g.Cache.Put(url, data)
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, last
}
