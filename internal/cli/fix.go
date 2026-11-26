package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// fixFlags are the flags of `deepward fix`.
type fixFlags struct {
	publish  bool
	platform string
	repo     string
	apiURL   string
}

func (f *fixFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.publish, "publish", false, "create or update merge/pull requests (default: dry run, print a diff)")
	fl.StringVar(&f.platform, "platform", "", "hosting platform: gitlab or github (default: detected from the remote URL)")
	fl.StringVar(&f.repo, "repo", "", "repository path on the platform, e.g. group/project or owner/repo")
	fl.StringVar(&f.apiURL, "api-url", "", "platform API URL for self-managed GitLab or GitHub Enterprise")
}

// token returns the platform token from the environment.
func (f *fixFlags) token(env Env, platform string) string {
	switch platform {
	case "gitlab":
		return env.getenv("DEEPWARD_GITLAB_TOKEN")
	case "github":
		return env.getenv("DEEPWARD_GITHUB_TOKEN")
	}
	return firstNonEmpty(env.getenv("DEEPWARD_GITLAB_TOKEN"), env.getenv("DEEPWARD_GITHUB_TOKEN"))
}

// check validates publish preconditions before anything else happens.
func (f *fixFlags) check(env Env) error {
	if f.publish && f.token(env, "") == "" {
		return usage(errors.New("--publish needs DEEPWARD_GITLAB_TOKEN or DEEPWARD_GITHUB_TOKEN; nothing was changed"))
	}
	return nil
}

func (s *session) fix(ctx context.Context, f *fixFlags) error {
	var platform sdk.Platform
	var cfg sdk.PlatformConfig
	if f.publish {
		var err error
		platform, cfg, err = s.platform(f)
		if err != nil {
			return err
		}
	}
	plan, err := s.eng.PlanFixes(ctx)
	if err != nil {
		return exitCode(ExitRuntime, err)
	}
	if !f.publish {
		printPlan(s.env, plan, s.g.quiet)
		return nil
	}
	client, err := platform.Connect(ctx, cfg)
	if err != nil {
		return exitCode(ExitRuntime, fmt.Errorf("connect to %s: %w", platform.Info().ID, err))
	}
	target := firstNonEmpty(s.cfg.Fix.TargetBranch, s.ci.DefaultBranch)
	if target == "" {
		if target, err = client.DefaultBranch(ctx); err != nil {
			return exitCode(ExitRuntime, fmt.Errorf("default branch: %w", err))
		}
	}
	res, err := s.eng.Publish(ctx, client, plan, target)
	if err != nil {
		return exitCode(ExitRuntime, err)
	}
	if !s.g.quiet {
		printPublish(s.env, plan, res)
	}
	return nil
}

// platform selects the hosting platform plugin and its configuration.
func (s *session) platform(f *fixFlags) (sdk.Platform, sdk.PlatformConfig, error) {
	id := firstNonEmpty(f.platform, s.cfg.Platform.Type, s.ci.Platform)
	var platforms []sdk.Platform
	for _, p := range s.eng.Enabled(sdk.KindPlatform) {
		if pl, ok := p.(sdk.Platform); ok {
			platforms = append(platforms, pl)
		}
	}
	var chosen sdk.Platform
	for _, p := range platforms {
		if (id != "" && p.Info().ID == id) || (id == "" && p.Detect(s.ci.RepoURL)) {
			chosen = p
			break
		}
	}
	if chosen == nil {
		if id == "" {
			return nil, sdk.PlatformConfig{}, usagef("cannot detect the hosting platform of %q; pass --platform gitlab or --platform github", s.ci.RepoURL)
		}
		return nil, sdk.PlatformConfig{}, usagef("unknown or disabled platform %q", id)
	}
	repo := firstNonEmpty(f.repo, s.cfg.Platform.Repo)
	if repo == "" && s.ci.Platform == chosen.Info().ID {
		repo = s.ci.Repo
	}
	if repo == "" {
		repo = repoFromURL(s.ci.RepoURL)
	}
	if repo == "" {
		return nil, sdk.PlatformConfig{}, usagef("cannot determine the repository; pass --repo or --repo-url")
	}
	api := firstNonEmpty(f.apiURL, s.cfg.Platform.APIURL)
	if api == "" && s.ci.Platform == chosen.Info().ID {
		api = s.ci.APIURL
	}
	token := f.token(s.env, chosen.Info().ID)
	if token == "" {
		return nil, sdk.PlatformConfig{}, usagef("--publish to %s needs DEEPWARD_%s_TOKEN; nothing was changed", chosen.Info().ID, strings.ToUpper(chosen.Info().ID))
	}
	return chosen, sdk.PlatformConfig{APIURL: api, Repo: repo, Token: token, RepoURL: s.ci.RepoURL}, nil
}

// repoFromURL turns "https://host/group/sub/project.git" into "group/sub/project".
func repoFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		if _, after, ok := strings.Cut(raw, ":"); ok {
			return strings.TrimSuffix(after, ".git")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
}

func printPlan(env Env, plan *engine.FixPlan, quiet bool) {
	w := env.Stdout
	for _, cs := range plan.ChangeSets {
		fmt.Fprintf(w, "### %s\nbranch: %s\nlabels: %s\n\n%s\n", cs.Title, cs.Branch, strings.Join(cs.Labels, ", "), cs.Diff)
	}
	if quiet {
		return
	}
	for _, m := range plan.Manual {
		fmt.Fprintf(w, "manual: %s %s (%s): %s\n", m.Project, m.Rule, m.Package, m.Reason)
		if m.Suggestion != "" {
			fmt.Fprintf(w, "  suggestion: %s\n", m.Suggestion)
		}
	}
	for _, f := range plan.Failed {
		fmt.Fprintf(w, "failed: %s %s: %s\n", f.Project, f.Rule, f.Reason)
	}
	fmt.Fprintf(w, "Dry run: %d change request(s) prepared, %d manual, %d verification failure(s). Nothing was written; add --publish to open them.\n",
		len(plan.ChangeSets), len(plan.Manual), len(plan.Failed))
}

func printPublish(env Env, plan *engine.FixPlan, res *engine.PublishResult) {
	w := env.Stdout
	for _, cr := range res.Created {
		fmt.Fprintf(w, "created: %s %s\n", cr.URL, cr.Title)
	}
	for _, cr := range res.Updated {
		fmt.Fprintf(w, "updated: %s %s\n", cr.URL, cr.Title)
	}
	for _, cr := range res.Paused {
		fmt.Fprintf(w, "paused (commits by others): %s\n", cr.URL)
	}
	for _, cr := range res.Closed {
		fmt.Fprintf(w, "closed (no longer needed): %s\n", cr.URL)
	}
	for _, cs := range res.Deferred {
		fmt.Fprintf(w, "deferred (limit reached): %s\n", cs.Title)
	}
	for _, m := range plan.Manual {
		fmt.Fprintf(w, "manual: %s %s: %s\n", m.Project, m.Rule, m.Reason)
	}
	for _, f := range plan.Failed {
		fmt.Fprintf(w, "failed: %s %s: %s\n", f.Project, f.Rule, f.Reason)
	}
}
