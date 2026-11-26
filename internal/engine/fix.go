package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/internal/textedit"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// FixItem is one package change in a change set.
type FixItem struct {
	Package string `json:"package"`
	From    string `json:"from"`
	To      string `json:"to"`
	Pin     bool   `json:"pin,omitempty"`
	// Kind is the finding kind that motivated the change (vulnerability, outdated, ...).
	Kind       sdk.FindingKind `json:"kind"`
	UpdateType string          `json:"updateType,omitempty"`
	// Findings are the findings this change fixes.
	Findings []sdk.Finding `json:"-"`
	Text     bool          `json:"text,omitempty"`
	// Upgrade is set when a security fix also takes the package to the
	// version an update proposed, so no separate update request is needed.
	Upgrade bool `json:"upgrade,omitempty"`
}

// ChangeSet is a verified set of edits for one project and group: the
// content of one merge/pull request.
type ChangeSet struct {
	Project     sdk.Project      `json:"project"`
	Group       string           `json:"group"`
	Branch      string           `json:"branch"`
	Title       string           `json:"title"`
	Items       []FixItem        `json:"items"`
	Files       []sdk.FileChange `json:"-"`
	Diff        string           `json:"-"`
	Security    bool             `json:"security"`
	MaxSeverity sdk.Severity     `json:"maxSeverity"`
	// Urgent is set for critical and high security fixes in built code; they
	// bypass the open request limit.
	Urgent bool `json:"urgent"`
	// NotBuilt is set when every finding of the change is in code that is
	// not built.
	NotBuilt bool     `json:"notBuilt"`
	Labels   []string `json:"labels"`
}

// ManualFix is a finding that needs a human.
type ManualFix struct {
	Project    string `json:"project"`
	Group      string `json:"group"`
	Rule       string `json:"rule"`
	Package    string `json:"package,omitempty"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"`
}

// FailedFix is a group whose edits did not pass verification.
type FailedFix struct {
	Project string `json:"project"`
	Group   string `json:"group"`
	Rule    string `json:"rule"`
	Reason  string `json:"reason"`
}

// FixPlan is the outcome of the fix pipeline.
type FixPlan struct {
	Result     *sdk.ScanResult `json:"-"`
	ChangeSets []ChangeSet     `json:"changeSets"`
	Manual     []ManualFix     `json:"manual"`
	Failed     []FailedFix     `json:"failed"`
}

// target is a set of findings sharing the same remediation alternatives.
type target struct {
	findings     []sdk.Finding
	alternatives []sdk.Remediation
	// upgrade is an update of the same package to a newer version, merged
	// into this security target. It is tried first and must also clear
	// upgradeFindings; otherwise the security alternatives are used and the
	// update is reported as failed under upgradeGroup.
	upgrade         *sdk.Remediation
	upgradeFindings []sdk.Finding
	upgradeGroup    string
}

type fixGroup struct {
	project sdk.Project
	name    string
	targets []*target
}

// PlanFixes scans the workspace, then prepares and verifies edits for every
// finding that carries remediations.
func (e *Engine) PlanFixes(ctx context.Context) (*FixPlan, error) {
	res, err := e.Scan(ctx)
	if err != nil {
		return nil, err
	}
	plan := &FixPlan{Result: res}
	projects := map[string]sdk.Project{}
	for _, p := range res.Projects {
		if p.Status == sdk.ProjectOK {
			projects[p.ID] = p.Project
		}
	}
	for _, g := range e.groups(res, projects) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cs, manual, failed, err := e.fixGroup(ctx, g)
		if err != nil {
			return nil, err
		}
		plan.Manual = append(plan.Manual, manual...)
		plan.Failed = append(plan.Failed, failed...)
		if cs != nil {
			plan.ChangeSets = append(plan.ChangeSets, *cs)
		}
	}
	sortChangeSets(plan.ChangeSets)
	if err := e.checkBranches(plan.ChangeSets); err != nil {
		return nil, err
	}
	return plan, nil
}

func sortChangeSets(cs []ChangeSet) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.NotBuilt != b.NotBuilt {
			return b.NotBuilt
		}
		if a.Security != b.Security {
			return a.Security
		}
		if ra, rb := a.MaxSeverity.Rank(), b.MaxSeverity.Rank(); ra != rb {
			return ra > rb
		}
		return a.Branch < b.Branch
	})
}

func (e *Engine) groupName(f sdk.Finding, r sdk.Remediation) string {
	if r.Package != nil {
		pkg := r.Package.PURL
		for _, g := range e.cfg.Fix.Groups {
			for _, pattern := range g.Match {
				if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(pkg)); ok || strings.EqualFold(pattern, pkg) {
					return g.Name
				}
			}
		}
	}
	switch e.cfg.Fix.Grouping {
	case GroupingProject:
		return GroupAll
	case GroupingType:
		switch f.Kind {
		case sdk.KindVulnerability:
			return GroupSecurity
		case sdk.KindOutdated:
			return GroupUpdates
		}
		return f.Analyzer
	}
	if r.Group != "" {
		return r.Group
	}
	return f.Analyzer + "/" + f.Rule
}

// Group names of the type and project grouping strategies.
const (
	GroupSecurity = "security"
	GroupUpdates  = "updates"
	GroupAll      = "all"
)

func altKey(alts []sdk.Remediation) string {
	var sb strings.Builder
	for _, a := range alts {
		if a.Package != nil {
			fmt.Fprintf(&sb, "%s@%s:%v;", a.Package.PURL, a.Package.To, a.Package.Pin)
		}
		for _, t := range a.TextEdits {
			fmt.Fprintf(&sb, "%s[%d,%d)=%s;", t.Path, t.Start, t.End, t.Replacement)
		}
		sb.WriteByte('|')
	}
	return sb.String()
}

func (e *Engine) groups(res *sdk.ScanResult, projects map[string]sdk.Project) []*fixGroup {
	byKey := map[string]*fixGroup{}
	var order []string
	targets := map[string]*target{}
	for _, f := range res.Findings {
		if f.Suppression != nil || len(f.Remediations) == 0 {
			continue
		}
		p, ok := projects[f.Project]
		if !ok {
			continue
		}
		name := e.groupName(f, f.Remediations[0])
		key := p.ID + "\x00" + name
		g, ok := byKey[key]
		if !ok {
			g = &fixGroup{project: p, name: name}
			byKey[key] = g
			order = append(order, key)
		}
		tk := key + "\x00" + altKey(f.Remediations)
		t, ok := targets[tk]
		if !ok {
			t = &target{alternatives: f.Remediations}
			targets[tk] = t
			g.targets = append(g.targets, t)
		}
		t.findings = append(t.findings, f)
	}
	sort.Strings(order)
	out := make([]*fixGroup, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return mergeUpdates(out)
}

// changeKey identifies a package change of a project: the package and the
// version it starts from.
func changeKey(p sdk.Project, c *sdk.PackageChange) string {
	pkg := c.PURL
	if u, err := purl.Parse(pkg); err == nil {
		pkg = u.WithoutVersion().String()
	}
	return p.ID + "\x00" + strings.ToLower(pkg) + "\x00" + c.From
}

func allKind(fs []sdk.Finding, kind sdk.FindingKind) bool {
	for _, f := range fs {
		if f.Kind != kind {
			return false
		}
	}
	return len(fs) > 0
}

// mergeUpdates folds the update of a package into the security fix that
// upgrades the same package from the same version, so one change request
// carries both instead of two requests editing the same line.
func mergeUpdates(groups []*fixGroup) []*fixGroup {
	security := map[string]*target{}
	for _, g := range groups {
		for _, t := range g.targets {
			if !allKind(t.findings, sdk.KindVulnerability) {
				continue
			}
			for _, alt := range t.alternatives {
				if alt.Package == nil || alt.Package.Pin {
					continue
				}
				if k := changeKey(g.project, alt.Package); security[k] == nil {
					security[k] = t
				}
			}
		}
	}
	out := groups[:0]
	for _, g := range groups {
		kept := g.targets[:0]
		for _, t := range g.targets {
			if allKind(t.findings, sdk.KindOutdated) && len(t.alternatives) == 1 && t.alternatives[0].Package != nil && !t.alternatives[0].Package.Pin {
				if s := security[changeKey(g.project, t.alternatives[0].Package)]; s != nil && s.upgrade == nil {
					alt := t.alternatives[0]
					s.upgrade, s.upgradeFindings, s.upgradeGroup = &alt, t.findings, g.name
					continue
				}
			}
			kept = append(kept, t)
		}
		g.targets = kept
		if len(kept) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// scratch is a private copy of the files a project needs.
type scratch struct {
	ws       *sdk.Workspace
	original map[string][]byte
}

func (e *Engine) newScratch(p sdk.Project) (*scratch, error) {
	dir, err := os.MkdirTemp("", "deepward-fix-")
	if err != nil {
		return nil, err
	}
	sc := &scratch{ws: sdk.NewWorkspace(dir, e.ws.Exclude), original: map[string][]byte{}}
	copyFile := func(rel string) error {
		data, err := fs.ReadFile(e.ws.FS, rel)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		sc.original[rel] = data
		return os.WriteFile(dst, data, 0o644)
	}
	root := path.Clean(p.Dir)
	err = fs.WalkDir(e.ws.FS, root, func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != root && e.ws.IsExcluded(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(rel)
	})
	if err == nil {
		for _, m := range p.Manifests {
			if _, done := sc.original[m]; !done {
				if err = copyFile(m); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		sc.close()
		return nil, fmt.Errorf("copy project %s: %w", p.ID, err)
	}
	return sc, nil
}

func (s *scratch) close() { _ = os.RemoveAll(s.ws.Root) }

func (s *scratch) read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.ws.Root, filepath.FromSlash(rel)))
}

func (s *scratch) write(rel string, data []byte) error {
	dst := filepath.Join(s.ws.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// apply applies edits and returns the previous contents for rollback.
func (s *scratch) apply(edits []sdk.TextEdit) (map[string][]byte, error) {
	prev := map[string][]byte{}
	for file, es := range textedit.ByFile(edits) {
		cur, err := s.read(file)
		if err != nil {
			return prev, err
		}
		next, err := textedit.Apply(cur, es)
		if err != nil {
			return prev, err
		}
		prev[file] = cur
		if err := s.write(file, next); err != nil {
			return prev, err
		}
	}
	return prev, nil
}

func (s *scratch) rollback(prev map[string][]byte) {
	for file, data := range prev {
		_ = s.write(file, data)
	}
}

func (s *scratch) hash() string {
	h := sha256.New()
	files := make([]string, 0, len(s.original))
	for f := range s.original {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		data, _ := s.read(f)
		h.Write([]byte(f))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (e *Engine) realize(ctx context.Context, sc *scratch, p sdk.Project, r sdk.Remediation) ([]sdk.TextEdit, *sdk.ManualChange) {
	if len(r.TextEdits) > 0 {
		return r.TextEdits, nil
	}
	rz, ok := e.enabled[p.Ecosystem].(sdk.RemediationRealizer)
	if !ok {
		return nil, &sdk.ManualChange{Change: *r.Package, Reason: fmt.Sprintf("ecosystem %s cannot edit manifests", p.Ecosystem)}
	}
	var out sdk.RealizeResult
	err := guard(ctx, e.pluginTimeout(), func(ctx context.Context) error {
		var err error
		out, err = rz.Realize(ctx, sdk.RealizeRequest{Project: p, Workspace: sc.ws, Changes: []sdk.PackageChange{*r.Package}})
		return err
	})
	if err != nil {
		return nil, &sdk.ManualChange{Change: *r.Package, Reason: "the ecosystem could not edit the manifests: " + err.Error()}
	}
	if len(out.Manual) > 0 {
		m := out.Manual[0]
		return nil, &m
	}
	if len(out.Edits) == 0 {
		return nil, &sdk.ManualChange{Change: *r.Package, Reason: "the ecosystem produced no edit"}
	}
	return out.Edits, nil
}

// verify rescans the scratch copy and reports whether the targeted findings
// are gone and the project still builds.
func (e *Engine) verify(ctx context.Context, sc *scratch, p sdk.Project, findings []sdk.Finding, cache map[string]*sdk.ScanResult) (bool, string, error) {
	analyzers := map[string]bool{}
	for _, f := range findings {
		analyzers[f.Analyzer] = true
	}
	ids := make([]string, 0, len(analyzers))
	for a := range analyzers {
		ids = append(ids, a)
	}
	sort.Strings(ids)
	key := sc.hash() + strings.Join(ids, ",")
	res, ok := cache[key]
	if !ok {
		var err error
		res, err = e.ScanWorkspace(ctx, sc.ws, ScanFilter{Projects: []string{p.ID}, Analyzers: ids})
		if err != nil {
			return false, "", err
		}
		cache[key] = res
	}
	for _, pr := range res.Projects {
		if pr.ID == p.ID && pr.Status != sdk.ProjectOK {
			return false, "the project no longer builds: " + pr.Reason, nil
		}
	}
	remaining := map[string]bool{}
	for _, f := range res.Findings {
		remaining[f.Fingerprint] = true
	}
	for _, f := range findings {
		if remaining[f.Fingerprint] {
			return false, f.Rule + " is still reported after the change", nil
		}
	}
	return true, "", nil
}

func (e *Engine) fixGroup(ctx context.Context, g *fixGroup) (*ChangeSet, []ManualFix, []FailedFix, error) {
	sc, err := e.newScratch(g.project)
	if err != nil {
		return nil, nil, nil, err
	}
	defer sc.close()
	cache := map[string]*sdk.ScanResult{}
	cs := &ChangeSet{Project: g.project, Group: g.name}
	var manual []ManualFix
	var failed []FailedFix
	for _, t := range g.targets {
		if t.upgrade != nil {
			all := append(append([]sdk.Finding{}, t.findings...), t.upgradeFindings...)
			it, m, why, err := e.attempt(ctx, sc, g.project, []sdk.Remediation{*t.upgrade}, all, cache)
			if err != nil {
				return nil, nil, nil, err
			}
			if it != nil {
				it.Upgrade = true
				if ut, ok := t.upgradeFindings[0].Properties["updateType"].(string); ok {
					it.UpdateType = ut
				}
				cs.Items = append(cs.Items, *it)
				continue
			}
			// The update is not possible: report it on its own and fall back
			// to the security fix alone.
			manual, failed = report(manual, failed, g.project.ID, t.upgradeGroup, t.upgradeFindings[0], m, why)
		}
		it, m, why, err := e.attempt(ctx, sc, g.project, t.alternatives, t.findings, cache)
		if err != nil {
			return nil, nil, nil, err
		}
		if it != nil {
			cs.Items = append(cs.Items, *it)
			continue
		}
		manual, failed = report(manual, failed, g.project.ID, g.name, t.findings[0], m, why)
	}
	if len(cs.Items) == 0 {
		return nil, manual, failed, nil
	}
	files := make([]string, 0, len(sc.original))
	for f := range sc.original {
		files = append(files, f)
	}
	sort.Strings(files)
	var diff strings.Builder
	for _, f := range files {
		cur, err := sc.read(f)
		if err != nil {
			return nil, nil, nil, err
		}
		if string(cur) != string(sc.original[f]) {
			cs.Files = append(cs.Files, sdk.FileChange{Path: f, Content: cur})
			diff.WriteString(textedit.Unified(f, sc.original[f], cur))
		}
	}
	if len(cs.Files) == 0 {
		return nil, manual, append(failed, FailedFix{Project: g.project.ID, Group: g.name, Rule: g.targets[0].findings[0].Rule,
			Reason: "verification-failed: the change touched no file"}), nil
	}
	cs.Diff = diff.String()
	e.describe(cs)
	return cs, manual, failed, nil
}

// attempt applies the first alternative that clears every finding and keeps
// the project building. Without one, it returns the last manual change or
// verification failure.
func (e *Engine) attempt(ctx context.Context, sc *scratch, p sdk.Project, alternatives []sdk.Remediation, findings []sdk.Finding,
	cache map[string]*sdk.ScanResult) (*FixItem, *sdk.ManualChange, string, error) {
	var lastManual *sdk.ManualChange
	var lastFailure string
	for _, alt := range alternatives {
		edits, m := e.realize(ctx, sc, p, alt)
		if m != nil {
			lastManual = m
			continue
		}
		prev, err := sc.apply(edits)
		if err != nil {
			sc.rollback(prev)
			lastFailure = err.Error()
			continue
		}
		ok, why, err := e.verify(ctx, sc, p, findings, cache)
		if err != nil {
			return nil, nil, "", err
		}
		if !ok {
			sc.rollback(prev)
			lastFailure = why
			continue
		}
		it := itemFor(alt, findings)
		return &it, nil, "", nil
	}
	return nil, lastManual, lastFailure, nil
}

// report records a finding that could not be fixed automatically.
func report(manual []ManualFix, failed []FailedFix, project, group string, first sdk.Finding, m *sdk.ManualChange, why string) ([]ManualFix, []FailedFix) {
	switch {
	case why != "":
		failed = append(failed, FailedFix{Project: project, Group: group, Rule: first.Rule, Reason: "verification-failed: " + why})
	case m != nil:
		mf := ManualFix{Project: project, Group: group, Rule: first.Rule, Reason: m.Reason, Suggestion: m.Suggestion}
		if p := first.PrimaryPackage(); p != nil {
			mf.Package = p.PURL
		}
		manual = append(manual, mf)
	}
	return manual, failed
}

func itemFor(alt sdk.Remediation, findings []sdk.Finding) FixItem {
	f := findings[0]
	it := FixItem{Kind: f.Kind, Findings: findings}
	if alt.Package != nil {
		it.Package, it.From, it.To, it.Pin = alt.Package.PURL, alt.Package.From, alt.Package.To, alt.Package.Pin
	} else {
		it.Text = true
		it.Package = alt.Description
	}
	if ut, ok := f.Properties["updateType"].(string); ok {
		it.UpdateType = ut
	}
	return it
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// Slug turns a group name into a branch-safe segment: the part after the
// last ':' of each path segment is kept ("security/org.x:lib" -> "security-lib").
func Slug(group string) string {
	parts := strings.Split(group, "/")
	for i, p := range parts {
		if j := strings.LastIndex(p, ":"); j >= 0 {
			p = p[j+1:]
		}
		parts[i] = p
	}
	s := slugRe.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "changes"
	}
	return s
}

func (e *Engine) describe(cs *ChangeSet) {
	cs.Branch = renderBranch(e.branchTemplate(), cs.Project.Ecosystem, dirSlug(cs.Project.Dir), Slug(cs.Group))
	cs.MaxSeverity = sdk.SeverityNone
	cs.NotBuilt = true
	for _, it := range cs.Items {
		for _, f := range it.Findings {
			if f.Kind == sdk.KindVulnerability {
				cs.Security = true
				if !f.NotBuilt && f.Severity.Normalize().AtLeast(sdk.SeverityHigh) {
					cs.Urgent = true
				}
				cs.NotBuilt = cs.NotBuilt && f.NotBuilt
			}
			if f.Severity.Normalize().Rank() > cs.MaxSeverity.Rank() {
				cs.MaxSeverity = f.Severity.Normalize()
			}
		}
	}
	// Only vulnerabilities can be "not built"; an update merged into a
	// security fix does not change that.
	cs.NotBuilt = cs.NotBuilt && cs.Security
	labels := e.cfg.Fix.Labels
	if labels == nil {
		labels = []string{"dependencies"}
	}
	cs.Labels = append([]string{}, labels...)
	if cs.Security {
		sec := e.cfg.Fix.SecurityLabels
		if sec == nil {
			sec = []string{"security"}
		}
		cs.Labels = append(cs.Labels, sec...)
	}
	cs.Title = "Deepward: " + e.titleText(cs)
	if cs.Project.Dir != "." && cs.Project.Dir != "" {
		cs.Title += " in " + cs.Project.Dir
	}
	if cs.NotBuilt {
		cs.Title += " (not built)"
	}
}

func (e *Engine) titleText(cs *ChangeSet) string {
	verb := "update"
	if cs.Security {
		verb = "fix vulnerabilities:"
	}
	if len(cs.Items) == 1 && !cs.Items[0].Text {
		it := cs.Items[0]
		return fmt.Sprintf("%s %s %s → %s", verb, packageName(it.Package), it.From, it.To)
	}
	if e.cfg.Fix.Grouping == "" || e.cfg.Fix.Grouping == GroupingPackage || e.ruleGroup(cs.Group) {
		return fmt.Sprintf("%s %s (%d changes)", verb, Slug(cs.Group), len(cs.Items))
	}
	var sec, upd, other int
	for _, it := range cs.Items {
		switch it.Kind {
		case sdk.KindVulnerability:
			sec++
		case sdk.KindOutdated:
			upd++
		default:
			other++
		}
	}
	var parts []string
	if sec > 0 {
		parts = append(parts, fmt.Sprintf("fix vulnerabilities in %d %s", sec, plural(sec, "package", "packages")))
	}
	if upd > 0 {
		parts = append(parts, fmt.Sprintf("update %d %s", upd, plural(upd, "dependency", "dependencies")))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s", other, plural(other, "change", "changes")))
	}
	return strings.Join(parts, ", ")
}

func (e *Engine) ruleGroup(name string) bool {
	for _, g := range e.cfg.Fix.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// packageName renders a purl as its ecosystem names the package
// (group:artifact, @scope/name, a Go module path, a NuGet ID).
func packageName(p string) string {
	u, err := purl.Parse(p)
	if err != nil {
		return p
	}
	return u.Coordinates()
}

// ErrNoChanges is returned by Publish when there is nothing to do.
var ErrNoChanges = errors.New("no changes")
