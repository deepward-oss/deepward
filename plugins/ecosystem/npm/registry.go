package npm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// registry is a configured npm registry.
type registry struct {
	URL      string
	Scope    string
	Token    string
	Username string
	Password string
}

// registryClient lists versions through the npm registry protocol.
type registryClient struct {
	registries []registry
	http       *deps.HTTPGetter
}

// forPackage selects the registry for a package: the one configured for its
// scope, otherwise the first unscoped registry.
func (c *registryClient) forPackage(name string) registry {
	scope := ""
	if strings.HasPrefix(name, "@") {
		scope, _, _ = strings.Cut(name, "/")
	}
	for _, r := range c.registries {
		if r.Scope != "" && r.Scope == scope {
			return r
		}
	}
	for _, r := range c.registries {
		if r.Scope == "" {
			return r
		}
	}
	return registry{URL: DefaultRegistry}
}

// Versions returns the published, non-deprecated versions of a package.
func (c *registryClient) Versions(ctx context.Context, name string) ([]string, error) {
	r := c.forPackage(name)
	h := http.Header{}
	h.Set("Accept", "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8")
	switch {
	case r.Token != "":
		h.Set("Authorization", "Bearer "+r.Token)
	case r.Username != "":
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.Username+":"+r.Password)))
	}
	// Scoped names keep the "@" and encode the slash: @scope%2fname.
	u := r.URL + "/" + strings.ReplaceAll(url.PathEscape(name), "%40", "@")
	data, status, err := c.http.Get(ctx, u, h)
	switch {
	case err != nil:
		return nil, fmt.Errorf("npm registry %s: %w", r.URL, err)
	case status == http.StatusNotFound:
		return nil, deps.ErrPackageNotFound
	case status != http.StatusOK:
		return nil, fmt.Errorf("npm registry %s: HTTP %d for %s", r.URL, status, name)
	}
	var doc struct {
		Versions map[string]struct {
			Deprecated json.RawMessage `json:"deprecated"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("npm registry %s: invalid metadata for %s: %w", r.URL, name, err)
	}
	var out []string
	for v, meta := range doc.Versions {
		d := bytes.TrimSpace(meta.Deprecated)
		if len(d) > 0 && !bytes.Equal(d, []byte("false")) && !bytes.Equal(d, []byte(`""`)) {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 && len(doc.Versions) == 0 {
		return nil, deps.ErrPackageNotFound
	}
	sort.Strings(out)
	return out, nil
}
