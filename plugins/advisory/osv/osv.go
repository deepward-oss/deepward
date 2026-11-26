// Package osv is the OSV.dev advisory source (online API).
package osv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/deps/osvschema"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
)

// DefaultAPI is the public OSV API.
const DefaultAPI = "https://api.osv.dev"

// Config is plugins.osv.
type Config struct {
	APIURL      string       `yaml:"apiUrl" json:"apiUrl"`
	Timeout     sdk.Duration `yaml:"timeout" json:"timeout"`
	BatchSize   int          `yaml:"batchSize" json:"batchSize"`
	Concurrency int          `yaml:"concurrency" json:"concurrency"`
	CacheTTL    sdk.Duration `yaml:"cacheTTL" json:"cacheTTL"`
}

// Validate implements sdk.Validator.
func (c *Config) Validate() error {
	if c.BatchSize < 0 || c.BatchSize > 1000 {
		return errors.New("batchSize must be between 1 and 1000")
	}
	if c.Concurrency < 0 {
		return errors.New("concurrency must be >= 1")
	}
	return nil
}

// Source queries OSV.dev.
type Source struct {
	cfg   Config
	HTTP  *http.Client
	cache *httpcache.Cache
	mu    sync.Mutex
	used  bool
}

func init() { sdk.Register(New()) }

// New returns the source with defaults.
func New() *Source {
	return &Source{cfg: Config{APIURL: DefaultAPI, Timeout: sdk.Duration(60 * time.Second), BatchSize: 1000, Concurrency: 8}}
}

// Info implements sdk.Plugin.
func (s *Source) Info() sdk.Info {
	return sdk.Info{ID: "osv", Kind: deps.KindAdvisorySource, Version: sdk.BuiltinVersion,
		Description: "OSV.dev vulnerability database (includes the GitHub Advisory Database for Maven and NuGet)"}
}

// ConfigTarget implements sdk.Configurable.
func (s *Source) ConfigTarget() any { return &s.cfg }

// Init implements sdk.Initializer.
func (s *Source) Init(_ context.Context, h sdk.Host) error {
	st := h.Settings()
	s.cache = &httpcache.Cache{Dir: st.CacheDir, TTL: s.cfg.CacheTTL.Std(), Disabled: st.NoCache || st.CacheDir == ""}
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: s.cfg.Timeout.Std()}
	}
	return nil
}

// DataLicense implements deps.AdvisorySource.
func (s *Source) DataLicense() deps.DataLicense {
	return deps.DataLicense{SPDX: "CC-BY-4.0", Attribution: "OSV.dev and its upstream databases (GitHub Advisory Database, CC-BY-4.0)",
		URL: "https://google.github.io/osv.dev/faq/#how-is-the-data-licensed"}
}

// Freshness implements deps.AdvisorySource: the API is always current.
func (s *Source) Freshness() *time.Time { return nil }

type batchQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version   string `json:"version"`
	PageToken string `json:"page_token,omitempty"`
}

type batchResponse struct {
	Results []struct {
		Vulns []struct {
			ID       string    `json:"id"`
			Modified time.Time `json:"modified"`
		} `json:"vulns"`
		NextPageToken string `json:"next_page_token"`
	} `json:"results"`
}

// Query implements deps.AdvisorySource.
func (s *Source) Query(ctx context.Context, queries []deps.PackageQuery) ([]deps.Advisory, error) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: s.cfg.Timeout.Std()}
	}
	s.mu.Lock()
	s.used = true
	s.mu.Unlock()
	pending := make([]batchQuery, 0, len(queries))
	for _, q := range queries {
		var bq batchQuery
		bq.Package.Name, bq.Package.Ecosystem, bq.Version = q.Name, string(q.Ecosystem), deps.AdvisoryVersion(q.Ecosystem, q.Version)
		pending = append(pending, bq)
	}
	ids := map[string]time.Time{}
	size := s.cfg.BatchSize
	if size <= 0 {
		size = 1000
	}
	for len(pending) > 0 {
		n := min(size, len(pending))
		chunk := pending[:n]
		pending = pending[n:]
		var resp batchResponse
		if err := s.post(ctx, "/v1/querybatch", map[string]any{"queries": chunk}, &resp); err != nil {
			return nil, err
		}
		for i, r := range resp.Results {
			for _, v := range r.Vulns {
				ids[v.ID] = v.Modified
			}
			if r.NextPageToken != "" && i < len(chunk) {
				next := chunk[i]
				next.PageToken = r.NextPageToken
				pending = append(pending, next)
			}
		}
	}
	return s.details(ctx, ids)
}

func (s *Source) details(ctx context.Context, ids map[string]time.Time) ([]deps.Advisory, error) {
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	out := make([]deps.Advisory, len(keys))
	errs := make([]error, len(keys))
	conc := s.cfg.Concurrency
	if conc <= 0 {
		conc = 8
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, id := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id string) {
			defer wg.Done()
			defer func() { <-sem }()
			cacheKey := fmt.Sprintf("osv:%s@%s", id, ids[id].UTC().Format(time.RFC3339Nano))
			data, ok := s.cache.Get(cacheKey)
			if !ok {
				var err error
				data, err = s.get(ctx, "/v1/vulns/"+id)
				if err != nil {
					errs[i] = err
					return
				}
				_ = s.cache.Put(cacheKey, data)
			}
			v, err := osvschema.Decode(data)
			if err != nil {
				errs[i] = fmt.Errorf("decode %s: %w", id, err)
				return
			}
			if v.IsWithdrawn() {
				return
			}
			out[i] = v.ToAdvisory("osv")
		}(i, id)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	res := out[:0]
	for _, a := range out {
		if a.ID != "" {
			res = append(res, a)
		}
	}
	return res, nil
}

func (s *Source) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.APIURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := s.do(req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("osv %s: invalid response: %w", path, err)
	}
	return nil
}

func (s *Source) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	return s.do(req)
}

func (s *Source) do(req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", "deepward")
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
			if req.GetBody != nil {
				req.Body, _ = req.GetBody()
			}
		}
		resp, err := s.HTTP.Do(req)
		if err != nil {
			last = fmt.Errorf("osv %s: %w", req.URL.Path, err)
			if req.Context().Err() != nil {
				return nil, last
			}
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		switch {
		case rerr != nil:
			last = rerr
		case resp.StatusCode == http.StatusOK:
			return data, nil
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			last = fmt.Errorf("osv %s: HTTP %d", req.URL.Path, resp.StatusCode)
		default:
			return nil, fmt.Errorf("osv %s: HTTP %d", req.URL.Path, resp.StatusCode)
		}
	}
	return nil, last
}

// Used reports whether Query was called (for data attribution).
func (s *Source) Used() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}
