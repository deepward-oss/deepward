package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultBranchTemplate names change-request branches. {project} is the
// project directory and is empty for a project at the repository root.
const DefaultBranchTemplate = "deepward/{ecosystem}/{project}/{group}"

// Grouping strategies for fixes that no group rule matches.
const (
	// GroupingPackage opens one change request per package.
	GroupingPackage = "package"
	// GroupingType opens one change request for security fixes and one for
	// other updates, per project.
	GroupingType = "type"
	// GroupingProject opens one change request per project.
	GroupingProject = "project"
)

var (
	placeholderRe = regexp.MustCompile(`\{[^{}]*\}`)
	branchTextRe  = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	placeholders  = map[string]bool{"{ecosystem}": true, "{project}": true, "{group}": true}
)

// ValidateBranchTemplate checks a branch name template: known placeholders
// only, {group} present, and a fixed prefix deepward uses to find its own
// change requests.
func ValidateBranchTemplate(tmpl string) error {
	for _, p := range placeholderRe.FindAllString(tmpl, -1) {
		if !placeholders[p] {
			return fmt.Errorf("unknown placeholder %s (use {ecosystem}, {project}, {group})", p)
		}
	}
	if !strings.Contains(tmpl, "{group}") {
		return errors.New("the template must contain {group}")
	}
	static := placeholderRe.ReplaceAllString(tmpl, "x")
	if !branchTextRe.MatchString(static) || strings.Contains(static, "..") || strings.Contains(static, "//") {
		return fmt.Errorf("%q is not a valid branch name: use letters, digits, '.', '_', '-' and '/'", tmpl)
	}
	prefix := branchPrefix(tmpl)
	if prefix == "" || strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, "-") || strings.HasPrefix(prefix, ".") {
		return errors.New("the template must start with a fixed prefix such as \"deepward/\"")
	}
	return nil
}

// branchPrefix is the fixed text before the first placeholder.
func branchPrefix(tmpl string) string {
	if i := strings.Index(tmpl, "{"); i >= 0 {
		return tmpl[:i]
	}
	return tmpl
}

func (e *Engine) branchTemplate() string {
	if e.cfg.Fix.Branch != "" {
		return e.cfg.Fix.Branch
	}
	return DefaultBranchTemplate
}

// renderBranch fills a template; an empty placeholder drops the separator
// next to it, so the root project gets "deepward/npm/security-axios".
func renderBranch(tmpl, ecosystem, project, group string) string {
	s := strings.NewReplacer("{ecosystem}", ecosystem, "{project}", project, "{group}", group).Replace(tmpl)
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return strings.Trim(s, "/")
}

// dirSlug turns a project directory into branch path segments
// ("services/API" -> "services/api"). The root directory gives "".
func dirSlug(dir string) string {
	var parts []string
	for _, seg := range strings.Split(strings.ReplaceAll(dir, "\\", "/"), "/") {
		seg = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(seg), "-"), "-.")
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "/")
}

// checkBranches fails when the template gives two change sets one branch.
func (e *Engine) checkBranches(cs []ChangeSet) error {
	seen := map[string]ChangeSet{}
	for _, c := range cs {
		if prev, ok := seen[c.Branch]; ok {
			return fmt.Errorf("fix.branch %q gives %s (%s) and %s (%s) the same branch %s; add {project} or {ecosystem} to the template",
				e.branchTemplate(), prev.Project.ID, prev.Group, c.Project.ID, c.Group, c.Branch)
		}
		seen[c.Branch] = c
	}
	return nil
}
