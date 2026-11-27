package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// TitleConfig is fix.title: one change request title pattern per kind of
// request. Patterns use {placeholder}s like fix.branch; a kind without a
// pattern keeps Deepward's default title.
type TitleConfig struct {
	// Security titles a request that fixes vulnerabilities with one package
	// change (an update merged into the fix included).
	Security string `yaml:"security"`
	// Pin titles a request that pins a transitive version (default: Security).
	Pin string `yaml:"pin"`
	// Update titles a request that updates one outdated dependency.
	Update string `yaml:"update"`
	// Group titles a request with several package changes.
	Group string `yaml:"group"`
}

// Kinds of change requests that fix.title patterns name.
const (
	TitleSecurity = "security"
	TitlePin      = "pin"
	TitleUpdate   = "update"
	TitleGroup    = "group"
)

// maxTitleLength bounds change request titles (GitLab allows 255 characters).
const maxTitleLength = 255

var titlePlaceholders = []string{"{package}", "{name}", "{from}", "{to}", "{count}", "{severity}", "{project}", "{ecosystem}", "{group}", "{update}"}

var (
	// emptyProjectRes remove an empty {project} with the brackets around it or
	// the preposition before it.
	emptyProjectRes = []*regexp.Regexp{
		regexp.MustCompile(`\(\s*\{project\}\s*\)`),
		regexp.MustCompile(`\[\s*\{project\}\s*\]`),
		regexp.MustCompile(`(?i)(?:^|\s+)(?:in|for|at)\s+\{project\}`),
	}
	spacesRe           = regexp.MustCompile(`\s+`)
	spaceBeforePunctRe = regexp.MustCompile(`\s+([:,])`)
)

// patterns lists the configured patterns by kind, in a stable order.
func (t TitleConfig) patterns() [][2]string {
	var out [][2]string
	for _, p := range [][2]string{{TitleSecurity, t.Security}, {TitlePin, t.Pin}, {TitleUpdate, t.Update}, {TitleGroup, t.Group}} {
		if p[1] != "" {
			out = append(out, p)
		}
	}
	return out
}

// pattern returns the pattern of a kind ("" when none applies).
func (t TitleConfig) pattern(kind string) string {
	switch kind {
	case TitleSecurity:
		return t.Security
	case TitlePin:
		if t.Pin != "" {
			return t.Pin
		}
		return t.Security
	case TitleUpdate:
		return t.Update
	case TitleGroup:
		return t.Group
	}
	return ""
}

// validate checks every pattern: known placeholders only, and a title that
// is not empty for a project at the repository root.
func (t TitleConfig) validate() []error {
	known := map[string]bool{}
	for _, p := range titlePlaceholders {
		known[p] = true
	}
	sample := titleValues{pkg: "org.example:lib", name: "lib", from: "1.0", to: "2.0", count: "2", severity: "high",
		ecosystem: "maven", group: "lib", update: "minor"}
	var errs []error
	for _, kp := range t.patterns() {
		kind, pattern := kp[0], kp[1]
		for _, ph := range placeholderRe.FindAllString(pattern, -1) {
			if !known[ph] {
				errs = append(errs, fmt.Errorf("fix.title.%s: unknown placeholder %s (use %s)", kind, ph, strings.Join(titlePlaceholders, ", ")))
			}
		}
		if renderTitle(pattern, sample) == "" {
			errs = append(errs, fmt.Errorf("fix.title.%s: %q renders an empty title for a project at the repository root", kind, pattern))
		}
	}
	return errs
}

// titleValues are the placeholder values of one change request.
type titleValues struct {
	pkg, name, from, to, count, severity, project, ecosystem, group, update string
}

// titleKind tells which fix.title pattern names a change set: "" for a
// request made only of text edits, which keeps the default title.
func titleKind(cs *ChangeSet) string {
	packages := 0
	for _, it := range cs.Items {
		if !it.Text {
			packages++
		}
	}
	switch {
	case packages == 0:
		return ""
	case len(cs.Items) > 1:
		return TitleGroup
	case cs.Items[0].Pin:
		return TitlePin
	case cs.Security:
		return TitleSecurity
	}
	return TitleUpdate
}

func titleValuesOf(cs *ChangeSet) titleValues {
	v := titleValues{count: strconv.Itoa(len(cs.Items)), ecosystem: cs.Project.Ecosystem, group: Slug(cs.Group)}
	if dir := cs.Project.Dir; dir != "." {
		v.project = dir
	}
	top := sdk.SeverityNone
	for _, it := range cs.Items {
		for _, f := range it.Findings {
			if s := f.Severity.Normalize(); f.Kind == sdk.KindVulnerability && s.Rank() > top.Rank() {
				top = s
			}
		}
	}
	if top != sdk.SeverityNone {
		v.severity = string(top)
	}
	if len(cs.Items) == 1 && !cs.Items[0].Text {
		it := cs.Items[0]
		v.pkg, v.from, v.to, v.update = packageName(it.Package), it.From, it.To, it.UpdateType
		v.name = v.pkg
		if i := strings.LastIndexAny(v.pkg, ":/"); i >= 0 && i < len(v.pkg)-1 {
			v.name = v.pkg[i+1:]
		}
	}
	return v
}

// renderTitle fills a pattern. An empty {project} is removed together with
// brackets around it or a preposition before it; whitespace is tidied up.
func renderTitle(pattern string, v titleValues) string {
	p := pattern
	if v.project == "" {
		for _, re := range emptyProjectRes {
			p = re.ReplaceAllString(p, "")
		}
	}
	s := strings.NewReplacer("{package}", v.pkg, "{name}", v.name, "{from}", v.from, "{to}", v.to, "{count}", v.count,
		"{severity}", v.severity, "{project}", v.project, "{ecosystem}", v.ecosystem, "{group}", v.group, "{update}", v.update).Replace(p)
	s = spacesRe.ReplaceAllString(s, " ")
	s = spaceBeforePunctRe.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// customTitle renders the fix.title pattern of a change set, if one applies.
func (e *Engine) customTitle(cs *ChangeSet) (string, bool) {
	pattern := e.cfg.Fix.Title.pattern(titleKind(cs))
	if pattern == "" {
		return "", false
	}
	if t := renderTitle(pattern, titleValuesOf(cs)); t != "" {
		return t, true
	}
	return "", false
}

// clampTitle cuts s to at most limit characters, ending with "…" when cut.
func clampTitle(s string, limit int) string {
	r := []rune(s)
	if limit < 1 || len(r) <= limit {
		return s
	}
	return strings.TrimSpace(string(r[:limit-1])) + "…"
}
