package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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
	// Unverified is set when the project still builds with the change but
	// the targeted findings are still reported (fix.unverified.publish).
	Unverified bool `json:"unverified,omitempty"`
	// VerifyFailure says why verification failed.
	VerifyFailure string `json:"verifyFailure,omitempty"`
	// Tasks are the post tasks that ran for the change request.
	Tasks []string `json:"tasks,omitempty"`
	// remedy is the remediation the item applies, realized again when the
	// change set is assembled.
	remedy sdk.Remediation
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
	NotBuilt bool `json:"notBuilt"`
	// Unverified is set when an item of the change failed verification.
	Unverified bool     `json:"unverified"`
	Labels     []string `json:"labels"`
	// CustomTitle is set when Title comes from a fix.title pattern; the
	// commit subject is then the title itself.
	CustomTitle bool `json:"-"`
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
	// Filtered is the number of findings with remediations that fix.kinds
	// or fix.minSeverity left out.
	Filtered int `json:"filtered,omitempty"`
	// GraphBuilds is the number of rescans verification ran, not counting
	// the scan before verification or results reused for identical files.
	GraphBuilds int `json:"-"`
	// excluded holds "project\x00group" of the groups made only of
	// filtered findings: Publish leaves their open requests alone.
	excluded map[string]bool
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
	groups, excluded, filtered := e.groups(res, projects)
	plan.excluded, plan.Filtered = excluded, filtered
	mode := e.cfg.Fix.VerifyMode()
	e.progress(ctx, "verifying fixes", "groups", len(groups), "filtered", filtered, "verify", mode)
	start := e.clock.Now()
	memo := newVerifyMemo()
	pool := e.startPool(len(groups), memo)
	var outcomes []groupOutcome
	if mode == VerifyEach {
		outcomes = e.fixGroups(ctx, pool, groups)
	} else {
		outcomes = e.fixBatches(ctx, pool, groups)
	}
	pool.close()
	if err := ctx.Err(); err != nil {
		// Verdicts of a cancelled run are not trustworthy.
		return nil, err
	}
	plan.GraphBuilds = memo.count()
	for _, o := range outcomes {
		if o.err != nil {
			return nil, o.err
		}
		plan.Manual = append(plan.Manual, o.manual...)
		plan.Failed = append(plan.Failed, o.failed...)
		if o.cs != nil {
			plan.ChangeSets = append(plan.ChangeSets, *o.cs)
		}
	}
	e.progress(ctx, "fixes verified", "changeSets", len(plan.ChangeSets), "manual", len(plan.Manual), "failed", len(plan.Failed),
		"graphBuilds", plan.GraphBuilds, "duration", e.since(start))
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

type groupOutcome struct {
	cs     *ChangeSet
	manual []ManualFix
	failed []FailedFix
	err    error
}

// fixPool runs fix jobs on fix.concurrency workers. Each worker copies a
// project once and reuses the copy for every job of that project; a job
// restores the edited files before it returns.
type fixPool struct {
	e    *Engine
	memo *verifyMemo
	jobs chan func(copies map[string]*scratch)
	wg   sync.WaitGroup
}

// startPool starts min(fix.concurrency, jobs) workers (at least one).
func (e *Engine) startPool(jobs int, memo *verifyMemo) *fixPool {
	workers := e.cfg.Fix.Concurrency
	if workers <= 0 {
		workers = DefaultFixConcurrency
	}
	workers = max(min(workers, jobs), 1)
	p := &fixPool{e: e, memo: memo, jobs: make(chan func(map[string]*scratch))}
	for range workers {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			copies := map[string]*scratch{}
			defer func() {
				for _, sc := range copies {
					sc.close()
				}
			}()
			for job := range p.jobs {
				job(copies)
			}
		}()
	}
	return p
}

// run runs job on a worker and waits for it.
func (p *fixPool) run(job func(copies map[string]*scratch)) {
	done := make(chan struct{})
	p.jobs <- func(copies map[string]*scratch) {
		defer close(done)
		job(copies)
	}
	<-done
}

func (p *fixPool) close() {
	close(p.jobs)
	p.wg.Wait()
}

// copyOf returns the worker's copy of a project, making it on first use.
func (p *fixPool) copyOf(copies map[string]*scratch, project sdk.Project) (*scratch, error) {
	if sc := copies[project.ID]; sc != nil {
		return sc, nil
	}
	sc, err := p.e.newScratch(project)
	if err != nil {
		return nil, err
	}
	sc.cache = p.memo
	copies[project.ID] = sc
	return sc, nil
}

// fixGroups verifies every group on its own (fix.verify: each). Outcomes
// keep the order of the groups.
func (e *Engine) fixGroups(ctx context.Context, pool *fixPool, groups []*fixGroup) []groupOutcome {
	out := make([]groupOutcome, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go pool.run(func(copies map[string]*scratch) {
			defer wg.Done()
			out[i] = e.fixGroupIn(ctx, pool, copies, g, fmt.Sprintf("%d/%d", i+1, len(groups)))
		})
	}
	wg.Wait()
	return out
}

// fixGroupIn verifies one group in the worker's copy of its project and
// assembles its change set in the same copy, which then holds only the
// group's verified changes.
func (e *Engine) fixGroupIn(ctx context.Context, pool *fixPool, copies map[string]*scratch, g *fixGroup, progress string) groupOutcome {
	if err := ctx.Err(); err != nil {
		return groupOutcome{err: err}
	}
	sc, err := pool.copyOf(copies, g.project)
	if err != nil {
		return groupOutcome{err: err}
	}
	start := e.clock.Now()
	e.progress(ctx, "verifying fix group", "group", g.name, "project", g.project.ID, "progress", progress)
	var o groupOutcome
	var items []FixItem
	items, o.manual, o.failed, o.err = e.fixGroup(ctx, sc, g)
	if o.err == nil && len(items) > 0 {
		var f *FailedFix
		o.cs, f, o.err = e.assemble(ctx, sc, g, items, false)
		if f != nil {
			o.failed = append(o.failed, *f)
		}
	}
	if err := sc.reset(); err != nil && o.err == nil {
		o.err = err
	}
	e.groupDone(ctx, g, o.cs, start)
	return o
}

func (e *Engine) groupDone(ctx context.Context, g *fixGroup, cs *ChangeSet, start time.Time) {
	result := "change set ready"
	if cs == nil {
		result = "no verified change"
	}
	e.progress(ctx, "fix group done", "group", g.name, "project", g.project.ID, "result", result, "duration", e.since(start))
}

// assemble turns a group's items into its change set: unless the copy
// already holds exactly them (apply false), it restores the copy and
// realizes and applies the items again; then it runs the post tasks once and
// takes the diff. A failing post task drops the change set.
func (e *Engine) assemble(ctx context.Context, sc *scratch, g *fixGroup, items []FixItem, apply bool) (*ChangeSet, *FailedFix, error) {
	failed := func(reason string) *FailedFix {
		return &FailedFix{Project: g.project.ID, Group: g.name, Rule: items[0].Findings[0].Rule, Reason: reason}
	}
	if apply {
		if err := sc.reset(); err != nil {
			return nil, nil, err
		}
		for _, it := range items {
			edits, m := e.realize(ctx, sc, g.project, it.remedy)
			if m != nil {
				return nil, failed("the change could not be applied again: " + m.Reason), nil
			}
			if prev, err := sc.apply(edits); err != nil {
				sc.rollback(prev)
				return nil, failed("the change could not be applied again: " + err.Error()), nil
			}
		}
	}
	tasks, err := e.runPostTasks(ctx, sc, g.project, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, failed(err.Error()), nil
	}
	for i := range items {
		items[i].Tasks = tasks
	}
	cs := &ChangeSet{Project: g.project, Group: g.name, Items: items}
	cs.Files, cs.Diff = sc.changes()
	if len(cs.Files) == 0 {
		return nil, failed("verification-failed: the change touched no file"), nil
	}
	e.describe(cs)
	return cs, nil, nil
}

// ordered sorts the alternatives of a finding by fix.transitive: with
// "upgrade" (the default) upgrades of the direct dependencies that bring a
// transitive package come first, with "pin" pins of its version. The order
// inside each kind is kept.
func (e *Engine) ordered(alts []sdk.Remediation) []sdk.Remediation {
	pinFirst := e.cfg.Fix.Transitive == TransitivePin
	isPin := func(r sdk.Remediation) bool { return r.Package != nil && r.Package.Pin }
	out := make([]sdk.Remediation, 0, len(alts))
	for _, first := range []bool{true, false} {
		for _, r := range alts {
			if (isPin(r) == pinFirst) == first {
				out = append(out, r)
			}
		}
	}
	return out
}

// fixable reports whether a finding passes fix.kinds and fix.minSeverity.
func (e *Engine) fixable(f sdk.Finding) bool {
	if kinds := e.cfg.Fix.Kinds; len(kinds) > 0 {
		ok := false
		for _, k := range kinds {
			ok = ok || strings.EqualFold(string(k), string(f.Kind))
		}
		if !ok {
			return false
		}
	}
	if least := e.cfg.Fix.MinSeverity; least != "" && f.Severity.Normalize().Rank() < least.Normalize().Rank() {
		return false
	}
	return true
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

// groups builds the fix groups of the findings that pass the fix filters.
// It also returns the groups made only of filtered findings and the number
// of filtered findings.
func (e *Engine) groups(res *sdk.ScanResult, projects map[string]sdk.Project) ([]*fixGroup, map[string]bool, int) {
	byKey := map[string]*fixGroup{}
	var order []string
	targets := map[string]*target{}
	excluded := map[string]bool{}
	filtered := 0
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
		if !e.fixable(f) {
			filtered++
			excluded[key] = true
			continue
		}
		g, ok := byKey[key]
		if !ok {
			g = &fixGroup{project: p, name: name}
			byKey[key] = g
			order = append(order, key)
		}
		alts := e.ordered(f.Remediations)
		tk := key + "\x00" + altKey(alts)
		t, ok := targets[tk]
		if !ok {
			t = &target{alternatives: alts}
			targets[tk] = t
			g.targets = append(g.targets, t)
		}
		t.findings = append(t.findings, f)
	}
	sort.Strings(order)
	out := make([]*fixGroup, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
		delete(excluded, k)
	}
	return mergeUpdates(out), excluded, filtered
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

// verdict is the outcome of verifying a change.
type verdict struct {
	ok  bool
	why string
	// remaining is set when the project builds but targeted findings are
	// still reported: the change may still help, so it can be published as
	// unverified.
	remaining bool
}

// verifyMemo holds verification scans by project, file contents and
// analyzers, shared by every copy of one fix run, and counts the rescans
// that actually ran.
type verifyMemo struct {
	mu      sync.Mutex
	results map[string]*sdk.ScanResult
	builds  int
}

func newVerifyMemo() *verifyMemo { return &verifyMemo{results: map[string]*sdk.ScanResult{}} }

func (m *verifyMemo) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.builds
}

// analyzersOf returns the sorted analyzers of the findings.
func analyzersOf(findings []sdk.Finding) []string {
	seen := map[string]bool{}
	var ids []string
	for _, f := range findings {
		if !seen[f.Analyzer] {
			seen[f.Analyzer] = true
			ids = append(ids, f.Analyzer)
		}
	}
	sort.Strings(ids)
	return ids
}

// rescan scans the project in the copy with the given analyzers, reusing
// the result of a scan of identical files.
func (e *Engine) rescan(ctx context.Context, sc *scratch, p sdk.Project, analyzers []string) (*sdk.ScanResult, error) {
	key := p.ID + "\x00" + sc.hash() + strings.Join(analyzers, ",")
	sc.cache.mu.Lock()
	res, ok := sc.cache.results[key]
	sc.cache.mu.Unlock()
	if ok {
		return res, nil
	}
	res, err := e.ScanWorkspace(withoutProgress(ctx), sc.ws, ScanFilter{Projects: []string{p.ID}, Analyzers: analyzers})
	if err != nil {
		return nil, err
	}
	sc.cache.mu.Lock()
	if _, ok := sc.cache.results[key]; !ok {
		sc.cache.results[key] = res
		sc.cache.builds++
	}
	sc.cache.mu.Unlock()
	return res, nil
}

// buildFailure returns why the project failed in a verification scan, or ""
// when it still builds.
func buildFailure(res *sdk.ScanResult, p sdk.Project) string {
	for _, pr := range res.Projects {
		if pr.ID == p.ID && pr.Status != sdk.ProjectOK {
			return "the project no longer builds: " + pr.Reason
		}
	}
	return ""
}

// verify rescans the scratch copy and reports whether the targeted findings
// are gone and the project still builds.
func (e *Engine) verify(ctx context.Context, sc *scratch, p sdk.Project, findings []sdk.Finding) (verdict, error) {
	res, err := e.rescan(ctx, sc, p, analyzersOf(findings))
	if err != nil {
		return verdict{}, err
	}
	if why := buildFailure(res, p); why != "" {
		return verdict{why: why}, nil
	}
	return judge(res, findings), nil
}

// judge reports whether the findings are gone from a scan of a project that
// builds.
func judge(res *sdk.ScanResult, findings []sdk.Finding) verdict {
	remaining := map[string]bool{}
	for _, f := range res.Findings {
		remaining[f.Fingerprint] = true
	}
	var still []string
	for _, f := range findings {
		if remaining[f.Fingerprint] && !contains(still, f.Rule) {
			still = append(still, f.Rule)
		}
	}
	if len(still) > 0 {
		verb := "is"
		if len(still) > 1 {
			verb = "are"
		}
		return verdict{why: strings.Join(still, ", ") + " " + verb + " still reported after the change", remaining: true}
	}
	return verdict{ok: true}
}

// phase is one list of alternatives tried for a target: the merged update
// first (which must also clear the update's findings), then the security
// alternatives.
type phase struct {
	alternatives []sdk.Remediation
	findings     []sdk.Finding
	upgrade      bool
	updateType   string
	// group and first are reported when no alternative of the phase passes.
	group string
	first sdk.Finding
}

func phases(g *fixGroup, t *target) []phase {
	var out []phase
	if t.upgrade != nil {
		ph := phase{alternatives: []sdk.Remediation{*t.upgrade}, findings: append(append([]sdk.Finding{}, t.findings...), t.upgradeFindings...),
			upgrade: true, group: t.upgradeGroup, first: t.upgradeFindings[0]}
		ph.updateType, _ = t.upgradeFindings[0].Properties["updateType"].(string)
		out = append(out, ph)
	}
	return append(out, phase{alternatives: t.alternatives, findings: t.findings, group: g.name, first: t.findings[0]})
}

// item returns the item of an alternative of the phase that passed.
func (ph phase) item(alt sdk.Remediation) FixItem {
	it := itemFor(alt, ph.findings)
	if ph.upgrade {
		it.Upgrade = true
		if ph.updateType != "" {
			it.UpdateType = ph.updateType
		}
	}
	return it
}

// fixGroup verifies the targets of a group one after the other in the copy
// (fix.verify: each). Accepted changes stay in the copy for the targets
// that follow. When the update merged into a target is not possible, it is
// reported on its own and the security alternatives are tried.
func (e *Engine) fixGroup(ctx context.Context, sc *scratch, g *fixGroup) ([]FixItem, []ManualFix, []FailedFix, error) {
	var items []FixItem
	var manual []ManualFix
	var failed []FailedFix
	for _, t := range g.targets {
		for _, ph := range phases(g, t) {
			alt, unverified, m, why, err := e.attempt(ctx, sc, g.project, ph.alternatives, ph.findings)
			if err != nil {
				return nil, nil, nil, err
			}
			if alt != nil {
				it := ph.item(*alt)
				if unverified != "" {
					it.Unverified, it.VerifyFailure = true, unverified
				}
				items = append(items, it)
				break
			}
			manual, failed = report(manual, failed, g.project.ID, ph.group, ph.first, m, why)
		}
	}
	return items, manual, failed, nil
}

// attempt applies the first alternative that clears every finding and keeps
// the project building, and returns it. Without one, it returns the last
// manual change or verification failure; with fix.unverified.publish, the
// first alternative (in the order of preference) that kept the project
// building is applied again and returned with why it is unverified.
func (e *Engine) attempt(ctx context.Context, sc *scratch, p sdk.Project, alternatives []sdk.Remediation,
	findings []sdk.Finding) (*sdk.Remediation, string, *sdk.ManualChange, string, error) {
	var lastManual *sdk.ManualChange
	var lastFailure string
	var candidate *sdk.Remediation
	var candidateWhy string
	for _, alt := range alternatives {
		start := e.clock.Now()
		e.progress(ctx, "verifying change", "project", p.ID, "change", alt.Description)
		v, m, err := e.try(ctx, sc, p, alt, findings)
		if err != nil {
			return nil, "", nil, "", err
		}
		if m != nil {
			lastManual = m
			continue
		}
		if !v.ok {
			lastFailure = v.why
			if v.remaining && candidate == nil {
				a := alt
				candidate, candidateWhy = &a, v.why
			}
			e.log.WarnContext(ctx, "change rejected", "project", p.ID, "change", alt.Description, "reason", v.why, "duration", e.since(start))
			continue
		}
		e.progress(ctx, "change verified", "project", p.ID, "change", alt.Description, "duration", e.since(start))
		return &alt, "", nil, "", nil
	}
	if candidate == nil || !e.cfg.Fix.Unverified.Publish {
		return nil, "", lastManual, lastFailure, nil
	}
	// Apply the candidate again (without verifying it) to publish it.
	edits, m := e.realize(ctx, sc, p, *candidate)
	if m != nil {
		return nil, "", m, lastFailure, nil
	}
	if prev, err := sc.apply(edits); err != nil {
		sc.rollback(prev)
		return nil, "", nil, lastFailure, nil
	}
	e.log.WarnContext(ctx, "publishing unverified change", "project", p.ID, "change", candidate.Description, "reason", candidateWhy)
	return candidate, candidateWhy, nil, "", nil
}

// try applies one alternative and verifies the result. A rejected change is
// rolled back.
func (e *Engine) try(ctx context.Context, sc *scratch, p sdk.Project, alt sdk.Remediation,
	findings []sdk.Finding) (verdict, *sdk.ManualChange, error) {
	edits, m := e.realize(ctx, sc, p, alt)
	if m != nil {
		return verdict{}, m, nil
	}
	prev, err := sc.apply(edits)
	if err != nil {
		sc.rollback(prev)
		return verdict{why: err.Error()}, nil, nil
	}
	v, err := e.verify(ctx, sc, p, findings)
	if err != nil {
		return verdict{}, nil, err
	}
	if !v.ok {
		sc.rollback(prev)
	}
	return v, nil, nil
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
	it := FixItem{Kind: f.Kind, Findings: findings, remedy: alt}
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
	for _, it := range cs.Items {
		cs.Unverified = cs.Unverified || it.Unverified
	}
	if cs.Unverified {
		un := e.cfg.Fix.Unverified.Labels
		if un == nil {
			un = DefaultUnverifiedLabels
		}
		cs.Labels = append(cs.Labels, un...)
	}
	// Markers are added after the text is cut to length, so they survive.
	text, custom := e.customTitle(cs)
	var prefix, suffix string
	if custom {
		if cs.Unverified {
			prefix = "[unverified] "
		}
	} else {
		prefix = "Deepward: "
		if cs.Unverified {
			prefix += "[unverified] "
		}
		text = e.titleText(cs)
		if cs.Project.Dir != "." && cs.Project.Dir != "" {
			text += " in " + cs.Project.Dir
		}
	}
	if cs.NotBuilt {
		suffix = " (not built)"
	}
	cs.CustomTitle = custom
	cs.Title = prefix + clampTitle(text, maxTitleLength-len([]rune(prefix))-len([]rune(suffix))) + suffix
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
