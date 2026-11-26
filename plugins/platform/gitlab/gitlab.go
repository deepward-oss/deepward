// Package gitlab publishes change requests as GitLab merge requests (API v4,
// gitlab.com and self-managed). Commits are created through the Commits API,
// so no git credentials are needed.
package gitlab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// ID is the plugin ID.
const ID = "gitlab"

// DefaultAPI is gitlab.com's API.
const DefaultAPI = "https://gitlab.com/api/v4"

// Platform is the GitLab platform.
type Platform struct {
	HTTP *http.Client
}

func init() { sdk.Register(&Platform{}) }

// Info implements sdk.Plugin.
func (p *Platform) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindPlatform, Version: sdk.BuiltinVersion,
		Description: "GitLab merge requests (gitlab.com and self-managed, API v4)"}
}

// Detect implements sdk.Platform.
func (p *Platform) Detect(repoURL string) bool {
	u, err := url.Parse(repoURL)
	host := ""
	if err == nil {
		host = u.Host
	}
	if strings.HasPrefix(repoURL, "git@") {
		host, _, _ = strings.Cut(strings.TrimPrefix(repoURL, "git@"), ":")
	}
	return strings.Contains(strings.ToLower(host), "gitlab")
}

// APIURL derives the API base from a repository URL.
func APIURL(repoURL string) string {
	if repoURL == "" {
		return DefaultAPI
	}
	if strings.HasPrefix(repoURL, "git@") {
		host, _, _ := strings.Cut(strings.TrimPrefix(repoURL, "git@"), ":")
		return "https://" + host + "/api/v4"
	}
	u, err := url.Parse(repoURL)
	if err != nil || u.Host == "" {
		return DefaultAPI
	}
	return u.Scheme + "://" + u.Host + "/api/v4"
}

// Connect implements sdk.Platform.
func (p *Platform) Connect(_ context.Context, cfg sdk.PlatformConfig) (sdk.PlatformClient, error) {
	if cfg.Token == "" {
		return nil, errors.New("a GitLab token is required")
	}
	if cfg.Repo == "" {
		return nil, errors.New("the GitLab project path is required")
	}
	api := cfg.APIURL
	if api == "" {
		api = APIURL(cfg.RepoURL)
	}
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{api: strings.TrimSuffix(api, "/"), project: url.PathEscape(cfg.Repo), token: cfg.Token, http: client}, nil
}

// Client talks to one GitLab project.
type Client struct {
	api, project, token string
	http                *http.Client
}

// apiError is a non-2xx response.
type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("gitlab: HTTP %d: %s", e.status, e.body) }

func (c *Client) do(ctx context.Context, method, path string, body any, out any) (http.Header, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("User-Agent", "deepward")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.Header, &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(data[:min(300, len(data))]))}
	}
	if out != nil {
		if raw, ok := out.(*[]byte); ok {
			*raw = data
			return resp.Header, nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("gitlab: invalid response from %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusNotFound
}

// DefaultBranch implements sdk.PlatformClient.
func (c *Client) DefaultBranch(ctx context.Context) (string, error) {
	var p struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/projects/"+c.project, nil, &p); err != nil {
		return "", err
	}
	return p.DefaultBranch, nil
}

// ReadFile implements sdk.PlatformClient.
func (c *Client) ReadFile(ctx context.Context, ref, path string) ([]byte, bool, error) {
	var data []byte
	_, err := c.do(ctx, http.MethodGet, "/projects/"+c.project+"/repository/files/"+url.PathEscape(path)+"/raw?ref="+url.QueryEscape(ref), nil, &data)
	if isNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

type mergeRequest struct {
	IID          int      `json:"iid"`
	WebURL       string   `json:"web_url"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Labels       []string `json:"labels"`
	State        string   `json:"state"`
}

func (m mergeRequest) toSDK() sdk.ChangeRequest {
	state := "open"
	if m.State != "opened" && m.State != "" {
		state = "closed"
	}
	return sdk.ChangeRequest{ID: strconv.Itoa(m.IID), URL: m.WebURL, Title: m.Title, Body: m.Description, SourceBranch: m.SourceBranch,
		TargetBranch: m.TargetBranch, Labels: m.Labels, State: state}
}

// ListChangeRequests implements sdk.PlatformClient.
func (c *Client) ListChangeRequests(ctx context.Context, prefix string) ([]sdk.ChangeRequest, error) {
	var out []sdk.ChangeRequest
	page := "1"
	for page != "" {
		var mrs []mergeRequest
		h, err := c.do(ctx, http.MethodGet, "/projects/"+c.project+"/merge_requests?state=opened&per_page=100&page="+page, nil, &mrs)
		if err != nil {
			return nil, err
		}
		for _, m := range mrs {
			if strings.HasPrefix(m.SourceBranch, prefix) {
				out = append(out, m.toSDK())
			}
		}
		page = h.Get("X-Next-Page")
	}
	return out, nil
}

// BranchCommits implements sdk.PlatformClient.
func (c *Client) BranchCommits(ctx context.Context, branch, base string) ([]sdk.CommitInfo, error) {
	var cmp struct {
		Commits []struct {
			ID          string `json:"id"`
			AuthorName  string `json:"author_name"`
			AuthorEmail string `json:"author_email"`
		} `json:"commits"`
	}
	_, err := c.do(ctx, http.MethodGet, "/projects/"+c.project+"/repository/compare?from="+url.QueryEscape(base)+"&to="+url.QueryEscape(branch), nil, &cmp)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]sdk.CommitInfo, 0, len(cmp.Commits))
	for i := len(cmp.Commits) - 1; i >= 0; i-- {
		cm := cmp.Commits[i]
		out = append(out, sdk.CommitInfo{SHA: cm.ID, AuthorName: cm.AuthorName, AuthorEmail: cm.AuthorEmail,
			IsBot: strings.EqualFold(cm.AuthorEmail, sdk.BotAuthor.Email)})
	}
	return out, nil
}

// Commit implements sdk.PlatformClient. With Force the branch is recreated
// from the base (GitLab's force option of the Commits API).
func (c *Client) Commit(ctx context.Context, req sdk.CommitRequest) (string, error) {
	type action struct {
		Action   string `json:"action"`
		FilePath string `json:"file_path"`
		Content  string `json:"content,omitempty"`
		Encoding string `json:"encoding,omitempty"`
	}
	var actions []action
	for _, f := range req.Files {
		if f.Delete {
			actions = append(actions, action{Action: "delete", FilePath: f.Path})
			continue
		}
		_, exists, err := c.ReadFile(ctx, req.BaseBranch, f.Path)
		if err != nil {
			return "", err
		}
		a := action{Action: "create", FilePath: f.Path, Content: base64.StdEncoding.EncodeToString(f.Content), Encoding: "base64"}
		if exists {
			a.Action = "update"
		}
		actions = append(actions, a)
	}
	body := map[string]any{"branch": req.Branch, "start_branch": req.BaseBranch, "commit_message": req.Message, "actions": actions,
		"author_name": sdk.BotAuthor.Name, "author_email": sdk.BotAuthor.Email, "force": req.Force}
	var out struct {
		ID string `json:"id"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/projects/"+c.project+"/repository/commits", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// CreateChangeRequest implements sdk.PlatformClient.
func (c *Client) CreateChangeRequest(ctx context.Context, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	var m mergeRequest
	body := map[string]any{"source_branch": spec.SourceBranch, "target_branch": spec.TargetBranch, "title": spec.Title,
		"description": spec.Body, "labels": strings.Join(spec.Labels, ","), "remove_source_branch": true}
	if _, err := c.do(ctx, http.MethodPost, "/projects/"+c.project+"/merge_requests", body, &m); err != nil {
		return sdk.ChangeRequest{}, err
	}
	return m.toSDK(), nil
}

// UpdateChangeRequest implements sdk.PlatformClient.
func (c *Client) UpdateChangeRequest(ctx context.Context, id string, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	var m mergeRequest
	body := map[string]any{"title": spec.Title, "description": spec.Body, "labels": strings.Join(spec.Labels, ",")}
	if _, err := c.do(ctx, http.MethodPut, "/projects/"+c.project+"/merge_requests/"+id, body, &m); err != nil {
		return sdk.ChangeRequest{}, err
	}
	return m.toSDK(), nil
}

// CloseChangeRequest implements sdk.PlatformClient.
func (c *Client) CloseChangeRequest(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPut, "/projects/"+c.project+"/merge_requests/"+id, map[string]any{"state_event": "close"}, nil)
	return err
}

// Comment implements sdk.PlatformClient.
func (c *Client) Comment(ctx context.Context, id, body string) error {
	_, err := c.do(ctx, http.MethodPost, "/projects/"+c.project+"/merge_requests/"+id+"/notes", map[string]any{"body": body}, nil)
	return err
}
