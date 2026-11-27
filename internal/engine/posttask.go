package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// PostTask is a command run once per change request, like Renovate's
// postUpgradeTasks: after the request's changes are verified, in a copy of
// the project that holds only them, and before they are committed. It can
// regenerate files that depend on dependency versions (generated code,
// checksums). Its changes to files matching Files go into the change
// request's commit; other changes are discarded.
type PostTask struct {
	// Tool is resolved by the toolchain like setup steps (mvn, npm, make, ...).
	Tool string            `yaml:"tool"`
	Args []string          `yaml:"args"`
	Env  map[string]string `yaml:"env"`
	// Dir is relative to the project directory (default: the project directory).
	Dir string `yaml:"dir"`
	// Files are glob patterns relative to the repository root ("**" matches
	// any number of directories) of the files whose changes are kept
	// (default: every file outside excluded directories such as target/).
	Files []string `yaml:"files"`
	// Ecosystems limits the task to projects of these ecosystems.
	Ecosystems []string `yaml:"ecosystems"`
	// Projects limits the task to these project directories.
	Projects []string `yaml:"projects"`
}

// String renders the command for logs and change request bodies.
func (t PostTask) String() string { return strings.TrimSpace(t.Tool + " " + strings.Join(t.Args, " ")) }

func (t PostTask) appliesTo(p sdk.Project) bool {
	in := func(list []string, v string) bool {
		if len(list) == 0 {
			return true
		}
		for _, x := range list {
			if strings.EqualFold(x, v) || path.Clean(x) == path.Clean(v) {
				return true
			}
		}
		return false
	}
	return in(t.Ecosystems, p.Ecosystem) && in(t.Projects, p.Dir)
}

// globRegexp compiles a slash-separated glob: "**" matches any path
// (including none), "*" and "?" match within one path segment.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	p := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(pattern)), "/")
	if p == "" {
		return nil, errors.New("empty pattern")
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*' && strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func compileGlobs(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := globRegexp(p)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// runPostTasks runs the post tasks that apply to the project in its copy
// and records the previous state of the files they change in prev (if not
// nil). It returns the commands run.
func (e *Engine) runPostTasks(ctx context.Context, sc *scratch, p sdk.Project, prev snapshot) ([]string, error) {
	var ran []string
	for _, t := range e.cfg.Fix.PostTasks {
		if !t.appliesTo(p) {
			continue
		}
		tool, err := e.tools.Require(ctx, sdk.ToolRequirement{Name: t.Tool, Purpose: "fix post task"}, &p)
		if err != nil {
			return ran, fmt.Errorf("post task %s: %w", t, err)
		}
		keep, err := compileGlobs(t.Files)
		if err != nil {
			return ran, fmt.Errorf("post task %s: files %w", t, err)
		}
		start := e.clock.Now()
		dir := filepath.Join(sc.ws.Root, filepath.FromSlash(path.Join(p.Dir, t.Dir)))
		res, err := tool.Run(ctx, sdk.RunSpec{Args: t.Args, Dir: dir, Env: t.Env})
		if err != nil {
			msg := fmt.Sprintf("post task %s failed: %v", t, err)
			if n := len(res.Tail); n > 0 {
				msg += ": " + res.Tail[n-1]
			}
			// Undo whatever the failed command left behind.
			_ = sc.sync(prev, nil)
			return ran, errors.New(msg)
		}
		if err := sc.sync(prev, keep); err != nil {
			return ran, fmt.Errorf("post task %s: %w", t, err)
		}
		e.progress(ctx, "post task done", "project", p.ID, "task", t.String(), "duration", e.since(start))
		ran = append(ran, t.String())
	}
	return ran, nil
}
