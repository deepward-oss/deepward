// Package github publishes change requests as GitHub pull requests (REST v3,
// github.com and Enterprise Server). Commits are created through the Git
// Data API, so no git credentials are needed.
package github

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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// ID is the plugin ID.
const ID = "github"

// DefaultAPI is github.com's API.
const DefaultAPI = "https://api.github.com"

// Platform is the GitHub platform.
type Platform struct {
	HTTP *http.Client
}

func init() { sdk.Register(&Platform{}) }

// Info implements sdk.Plugin.
func (p *Platform) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindPlatform, Version: sdk.BuiltinVersion,
		Description: "GitHub pull requests (github.com and Enterprise Server, REST v3)"}
}

func host(repoURL string) string {
	if strings.HasPrefix(repoURL, "git@") {
		h, _, _ := strings.Cut(strings.TrimPrefix(repoURL, "git@"), ":")
		return h
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// Detect implements sdk.Platform.
func (p *Platform) Detect(repoURL string) bool {
	return strings.Contains(strings.ToLower(host(repoURL)), "github")
}

// APIURL derives the API base from a repository URL.
func APIURL(repoURL string) string {
	h := host(repoURL)
	if h == "" || h == "github.com" || h == "www.github.com" {
		return DefaultAPI
	}
	return "https://" + h + "/api/v3"
}

// Connect implements sdk.Platform.
func (p *Platform) Connect(_ context.Context, cfg sdk.PlatformConfig) (sdk.PlatformClient, error) {
	if cfg.Token == "" {
		return nil, errors.New("a GitHub token is required")
	}
	owner, repo, ok := strings.Cut(cfg.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("the GitHub repository must be owner/repo, got %q", cfg.Repo)
	}
	api := cfg.APIURL
	if api == "" {
		api = APIURL(cfg.RepoURL)
	}
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{api: strings.TrimSuffix(api, "/"), repo: "/repos/" + owner + "/" + repo, token: cfg.Token, http: client}, nil
}

// Client talks to one repository.
type Client struct {
	api, repo, token string
	http             *http.Client
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.status, e.body) }

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path, accept string, body any, out any) (http.Header, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.api + path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "deepward")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
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
			return nil, fmt.Errorf("github: invalid response from %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

// DefaultBranch implements sdk.PlatformClient.
func (c *Client) DefaultBranch(ctx context.Context) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.repo, "", nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

// ReadFile implements sdk.PlatformClient.
func (c *Client) ReadFile(ctx context.Context, ref, path string) ([]byte, bool, error) {
	var data []byte
	_, err := c.do(ctx, http.MethodGet, c.repo+"/contents/"+escapePath(path)+"?ref="+url.QueryEscape(ref), "application/vnd.github.raw", nil, &data)
	if isNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

type pull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (p pull) toSDK() sdk.ChangeRequest {
	cr := sdk.ChangeRequest{ID: strconv.Itoa(p.Number), URL: p.HTMLURL, Title: p.Title, Body: p.Body, SourceBranch: p.Head.Ref, TargetBranch: p.Base.Ref, State: "open"}
	if p.State == "closed" {
		cr.State = "closed"
	}
	for _, l := range p.Labels {
		cr.Labels = append(cr.Labels, l.Name)
	}
	return cr
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// ListChangeRequests implements sdk.PlatformClient.
func (c *Client) ListChangeRequests(ctx context.Context, prefix string) ([]sdk.ChangeRequest, error) {
	var out []sdk.ChangeRequest
	next := c.repo + "/pulls?state=open&per_page=100"
	for next != "" {
		var pulls []pull
		h, err := c.do(ctx, http.MethodGet, next, "", nil, &pulls)
		if err != nil {
			return nil, err
		}
		for _, p := range pulls {
			if strings.HasPrefix(p.Head.Ref, prefix) {
				out = append(out, p.toSDK())
			}
		}
		next = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return out, nil
}

// BranchCommits implements sdk.PlatformClient.
func (c *Client) BranchCommits(ctx context.Context, branch, base string) ([]sdk.CommitInfo, error) {
	var cmp struct {
		Commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Author struct {
					Name  string `json:"name"`
					Email string `json:"email"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"commits"`
	}
	_, err := c.do(ctx, http.MethodGet, c.repo+"/compare/"+url.PathEscape(base)+"..."+url.PathEscape(branch), "", nil, &cmp)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]sdk.CommitInfo, 0, len(cmp.Commits))
	for i := len(cmp.Commits) - 1; i >= 0; i-- {
		cm := cmp.Commits[i]
		out = append(out, sdk.CommitInfo{SHA: cm.SHA, AuthorName: cm.Commit.Author.Name, AuthorEmail: cm.Commit.Author.Email,
			IsBot: strings.EqualFold(cm.Commit.Author.Email, sdk.BotAuthor.Email)})
	}
	return out, nil
}

// Commit implements sdk.PlatformClient: blobs → tree → commit → ref.
func (c *Client) Commit(ctx context.Context, req sdk.CommitRequest) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.repo+"/git/ref/heads/"+escapePath(req.BaseBranch), "", nil, &ref); err != nil {
		return "", fmt.Errorf("base branch %s: %w", req.BaseBranch, err)
	}
	baseSHA := ref.Object.SHA
	var baseCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.repo+"/git/commits/"+baseSHA, "", nil, &baseCommit); err != nil {
		return "", err
	}
	type entry struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"`
	}
	var tree []entry
	for _, f := range req.Files {
		e := entry{Path: f.Path, Mode: "100644", Type: "blob"}
		if !f.Delete {
			var blob struct {
				SHA string `json:"sha"`
			}
			if _, err := c.do(ctx, http.MethodPost, c.repo+"/git/blobs", "", map[string]string{"content": base64.StdEncoding.EncodeToString(f.Content), "encoding": "base64"}, &blob); err != nil {
				return "", err
			}
			e.SHA = &blob.SHA
		}
		tree = append(tree, e)
	}
	var newTree struct {
		SHA string `json:"sha"`
	}
	if _, err := c.do(ctx, http.MethodPost, c.repo+"/git/trees", "", map[string]any{"base_tree": baseCommit.Tree.SHA, "tree": tree}, &newTree); err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	author := map[string]string{"name": sdk.BotAuthor.Name, "email": sdk.BotAuthor.Email}
	if _, err := c.do(ctx, http.MethodPost, c.repo+"/git/commits", "", map[string]any{"message": req.Message, "tree": newTree.SHA,
		"parents": []string{baseSHA}, "author": author}, &commit); err != nil {
		return "", err
	}
	_, err := c.do(ctx, http.MethodGet, c.repo+"/git/ref/heads/"+escapePath(req.Branch), "", nil, nil)
	switch {
	case err == nil:
		if !req.Force {
			return "", fmt.Errorf("branch %s already exists", req.Branch)
		}
		_, err = c.do(ctx, http.MethodPatch, c.repo+"/git/refs/heads/"+escapePath(req.Branch), "", map[string]any{"sha": commit.SHA, "force": true}, nil)
	case isNotFound(err):
		_, err = c.do(ctx, http.MethodPost, c.repo+"/git/refs", "", map[string]any{"ref": "refs/heads/" + req.Branch, "sha": commit.SHA}, nil)
	}
	if err != nil {
		return "", err
	}
	return commit.SHA, nil
}

// CreateChangeRequest implements sdk.PlatformClient.
func (c *Client) CreateChangeRequest(ctx context.Context, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	var p pull
	if _, err := c.do(ctx, http.MethodPost, c.repo+"/pulls", "", map[string]any{"title": spec.Title, "head": spec.SourceBranch,
		"base": spec.TargetBranch, "body": spec.Body}, &p); err != nil {
		return sdk.ChangeRequest{}, err
	}
	cr := p.toSDK()
	if len(spec.Labels) > 0 {
		if _, err := c.do(ctx, http.MethodPost, c.repo+"/issues/"+cr.ID+"/labels", "", map[string]any{"labels": spec.Labels}, nil); err != nil {
			return cr, err
		}
		cr.Labels = spec.Labels
	}
	return cr, nil
}

// UpdateChangeRequest implements sdk.PlatformClient.
func (c *Client) UpdateChangeRequest(ctx context.Context, id string, spec sdk.ChangeRequestSpec) (sdk.ChangeRequest, error) {
	var p pull
	if _, err := c.do(ctx, http.MethodPatch, c.repo+"/pulls/"+id, "", map[string]any{"title": spec.Title, "body": spec.Body}, &p); err != nil {
		return sdk.ChangeRequest{}, err
	}
	cr := p.toSDK()
	if len(spec.Labels) > 0 {
		if _, err := c.do(ctx, http.MethodPut, c.repo+"/issues/"+id+"/labels", "", map[string]any{"labels": spec.Labels}, nil); err != nil {
			return cr, err
		}
		cr.Labels = spec.Labels
	}
	return cr, nil
}

// CloseChangeRequest implements sdk.PlatformClient.
func (c *Client) CloseChangeRequest(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPatch, c.repo+"/pulls/"+id, "", map[string]any{"state": "closed"}, nil)
	return err
}

// Comment implements sdk.PlatformClient.
func (c *Client) Comment(ctx context.Context, id, body string) error {
	_, err := c.do(ctx, http.MethodPost, c.repo+"/issues/"+id+"/comments", "", map[string]any{"body": body}, nil)
	return err
}
