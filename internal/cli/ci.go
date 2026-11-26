package cli

import (
	"encoding/json"
	"os"
	"strings"
)

// CIContext is what we know about the CI run.
type CIContext struct {
	Provider      string
	RepoURL       string
	Commit        string
	Branch        string
	DefaultBranch string
	// Platform, APIURL and Repo describe the hosting platform if known.
	Platform string
	APIURL   string
	Repo     string
}

// detectCI fills a CIContext from standard environment variables.
func detectCI(getenv func(string) string) CIContext {
	switch {
	case getenv("GITLAB_CI") == "true":
		return CIContext{
			Provider: "gitlab-ci", RepoURL: getenv("CI_PROJECT_URL"), Commit: getenv("CI_COMMIT_SHA"),
			Branch: firstNonEmpty(getenv("CI_COMMIT_BRANCH"), getenv("CI_COMMIT_REF_NAME")), DefaultBranch: getenv("CI_DEFAULT_BRANCH"),
			Platform: "gitlab", APIURL: getenv("CI_API_V4_URL"), Repo: getenv("CI_PROJECT_PATH"),
		}
	case getenv("GITHUB_ACTIONS") == "true":
		server := firstNonEmpty(getenv("GITHUB_SERVER_URL"), "https://github.com")
		repo := getenv("GITHUB_REPOSITORY")
		c := CIContext{
			Provider: "github-actions", Commit: getenv("GITHUB_SHA"), Branch: getenv("GITHUB_REF_NAME"),
			Platform: "github", APIURL: getenv("GITHUB_API_URL"), Repo: repo,
			DefaultBranch: githubDefaultBranch(getenv("GITHUB_EVENT_PATH")),
		}
		if repo != "" {
			c.RepoURL = strings.TrimSuffix(server, "/") + "/" + repo
		}
		if head := getenv("GITHUB_HEAD_REF"); head != "" {
			c.Branch = head
		}
		return c
	case getenv("CI") != "":
		return CIContext{Provider: "generic"}
	}
	return CIContext{}
}

func githubDefaultBranch(eventPath string) string {
	if eventPath == "" {
		return ""
	}
	b, err := os.ReadFile(eventPath)
	if err != nil {
		return ""
	}
	var ev struct {
		Repository struct {
			DefaultBranch string `json:"default_branch"`
		} `json:"repository"`
	}
	if json.Unmarshal(b, &ev) != nil {
		return ""
	}
	return ev.Repository.DefaultBranch
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// override applies explicit values (from flags) on top of detection.
func (c CIContext) override(repoURL, commit, branch, target string) CIContext {
	if repoURL != "" {
		c.RepoURL = repoURL
	}
	if commit != "" {
		c.Commit = commit
	}
	if branch != "" {
		c.Branch = branch
	}
	if target != "" {
		c.DefaultBranch = target
	}
	return c
}
