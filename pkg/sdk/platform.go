package sdk

import "context"

// Platform is a git hosting service on which change requests (GitLab merge
// requests, GitHub pull requests) are published.
type Platform interface {
	Plugin
	// Detect reports whether a repository URL belongs to this platform when
	// it is not configured explicitly (e.g. github.com).
	Detect(repoURL string) bool
	Connect(ctx context.Context, cfg PlatformConfig) (PlatformClient, error)
}

// PlatformConfig identifies the target repository.
type PlatformConfig struct {
	// APIURL is the API base URL (empty for the platform's public default).
	APIURL string
	// Repo is the project path ("group/sub/project" or "owner/repo").
	Repo  string
	Token string
	// RepoURL is the repository's web URL; platforms derive the API URL of
	// self-managed instances from it when APIURL is empty.
	RepoURL string
}

// BotAuthor is the commit author of deepward's commits; commits by anyone
// else pause automatic updates of a change request.
var BotAuthor = struct{ Name, Email string }{Name: "Deepward", Email: "deepward-bot@users.noreply.deepward.dev"}

// ChangeRequest is an open or closed MR/PR.
type ChangeRequest struct {
	ID           string   `json:"id"` // MR iid / PR number
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	SourceBranch string   `json:"sourceBranch"`
	TargetBranch string   `json:"targetBranch"`
	Labels       []string `json:"labels,omitempty"`
	State        string   `json:"state"` // "open" or "closed"
}

// ChangeRequestSpec is the desired content of an MR/PR.
type ChangeRequestSpec struct {
	Title        string
	Body         string
	SourceBranch string
	TargetBranch string
	Labels       []string
}

// FileChange is the full new content of one file.
type FileChange struct {
	Path    string
	Content []byte
	Delete  bool
}

// CommitRequest creates (or recreates) a branch with one commit on top of a base.
type CommitRequest struct {
	Branch     string
	BaseBranch string
	Message    string
	Files      []FileChange
	// Force replaces an existing branch (force-push semantics).
	Force bool
}

// CommitInfo is a commit on a branch.
type CommitInfo struct {
	SHA         string
	AuthorName  string
	AuthorEmail string
	// IsBot is true for commits authored by the token's user.
	IsBot bool
}

// PlatformClient performs operations on one repository.
type PlatformClient interface {
	// DefaultBranch returns the repository's default branch.
	DefaultBranch(ctx context.Context) (string, error)
	// ReadFile reads a file at a ref; ok is false if it does not exist.
	ReadFile(ctx context.Context, ref, path string) (content []byte, ok bool, err error)
	// ListChangeRequests returns open change requests whose source branch
	// starts with prefix.
	ListChangeRequests(ctx context.Context, branchPrefix string) ([]ChangeRequest, error)
	// BranchCommits returns commits on branch that are not on base, newest first.
	BranchCommits(ctx context.Context, branch, base string) ([]CommitInfo, error)
	Commit(ctx context.Context, req CommitRequest) (sha string, err error)
	CreateChangeRequest(ctx context.Context, spec ChangeRequestSpec) (ChangeRequest, error)
	UpdateChangeRequest(ctx context.Context, id string, spec ChangeRequestSpec) (ChangeRequest, error)
	CloseChangeRequest(ctx context.Context, id string) error
	Comment(ctx context.Context, id, body string) error
}
