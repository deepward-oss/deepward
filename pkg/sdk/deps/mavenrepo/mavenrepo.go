// Package mavenrepo reads version metadata from Maven repositories (Maven
// Central, Nexus, Artifactory). It is shared by the maven and sbt plugins.
package mavenrepo

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/httpcache"
)

// CentralURL is Maven Central.
const CentralURL = "https://repo.maven.apache.org/maven2"

// Repository is a Maven repository with optional credentials.
type Repository struct {
	ID       string
	URL      string
	Username string
	Password string
	// Token is sent as a bearer token when set.
	Token string
}

// Client queries repositories in order.
type Client struct {
	Repositories []Repository
	HTTP         *http.Client
	Cache        *httpcache.Cache
	// Retries is the number of retries after a 5xx or network error (default 3).
	Retries int
	// Backoff is the initial delay between retries (default 500ms, doubled).
	Backoff time.Duration
	// UserAgent is sent with every request.
	UserAgent string
}

type metadata struct {
	Versioning struct {
		Latest   string   `xml:"latest"`
		Release  string   `xml:"release"`
		Versions []string `xml:"versions>version"`
	} `xml:"versioning"`
}

// ParseMetadata extracts the version list of a maven-metadata.xml document.
func ParseMetadata(data []byte) ([]string, error) {
	var m metadata
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid maven-metadata.xml: %w", err)
	}
	return m.Versioning.Versions, nil
}

// RepoError describes a repository failure.
type RepoError struct {
	Repo   string
	Status int
	Err    error
}

func (e *RepoError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("repository %s: HTTP %d", e.Repo, e.Status)
	}
	return fmt.Sprintf("repository %s: %v", e.Repo, e.Err)
}

func (e *RepoError) Unwrap() error { return e.Err }

// Versions returns the merged, sorted version list of group:artifact. It
// returns deps.ErrPackageNotFound when no repository knows the artifact and
// an error when the artifact was not found and some repository failed.
func (c *Client) Versions(ctx context.Context, group, artifact string) ([]string, error) {
	path := strings.ReplaceAll(group, ".", "/") + "/" + artifact + "/maven-metadata.xml"
	seen := map[string]bool{}
	var out []string
	var errs []error
	for _, r := range c.repositories() {
		data, err := c.get(ctx, r, path)
		if err != nil {
			var re *RepoError
			if errors.As(err, &re) && re.Status == http.StatusNotFound {
				continue
			}
			errs = append(errs, err)
			continue
		}
		vs, err := ParseMetadata(data)
		if err != nil {
			errs = append(errs, &RepoError{Repo: r.ID, Err: err})
			continue
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" && !seen[v] {
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

// FetchPOM returns the POM of a released artifact version from the first
// repository that has it.
func (c *Client) FetchPOM(ctx context.Context, group, artifact, version string) ([]byte, error) {
	path := fmt.Sprintf("%s/%s/%s/%s-%s.pom", strings.ReplaceAll(group, ".", "/"), artifact, version, artifact, version)
	var errs []error
	for _, r := range c.repositories() {
		data, err := c.get(ctx, r, path)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, deps.ErrPackageNotFound
	}
	return nil, errors.Join(errs...)
}

func (c *Client) repositories() []Repository {
	if len(c.Repositories) == 0 {
		return []Repository{{ID: "central", URL: CentralURL}}
	}
	return c.Repositories
}

func (c *Client) get(ctx context.Context, r Repository, path string) ([]byte, error) {
	url := strings.TrimSuffix(r.URL, "/") + "/" + path
	if b, ok := c.Cache.Get(url); ok {
		return b, nil
	}
	retries := c.Retries
	if retries <= 0 {
		retries = 3
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		data, status, err := c.do(ctx, client, r, url)
		switch {
		case err == nil && status == http.StatusOK:
			_ = c.Cache.Put(url, data)
			return data, nil
		case err == nil && status >= 500:
			last = &RepoError{Repo: r.ID, Status: status}
			continue
		case err == nil:
			return nil, &RepoError{Repo: r.ID, Status: status}
		default:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &RepoError{Repo: r.ID, Err: err}
		}
	}
	return nil, last
}

func (c *Client) do(ctx context.Context, client *http.Client, r Repository, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	switch {
	case r.Token != "":
		req.Header.Set("Authorization", "Bearer "+r.Token)
	case r.Username != "":
		req.SetBasicAuth(r.Username, r.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, 0, err
	}
	return data, resp.StatusCode, nil
}
