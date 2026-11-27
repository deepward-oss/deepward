package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Batch verification (fix.verify: batch). Per project, verification runs in
// rounds: each round takes the next untried alternative (a unit) of every
// target without a verified change, splits the groups that have units into
// batches whose affected packages do not overlap and rebuilds the graph once
// per batch. A batch that does not build is split in halves until the units
// that break it are found.

// targetRun is the verification state of one target.
type targetRun struct {
	phases []phase
	ph     int // current phase
	next   int // next alternative of the current phase

	lastManual   *sdk.ManualChange
	lastFailure  string
	candidate    *sdk.Remediation
	candidateWhy string

	// item is the accepted (or unverified) change; it stays applied in
	// later rounds of its group.
	item   *FixItem
	done   bool
	manual []ManualFix
	failed []FailedFix
}

// groupRun is the verification state of one group.
type groupRun struct {
	g       *fixGroup
	targets []*targetRun
}

// unit is one alternative of one target, verified in the current round.
type unit struct {
	tr  *targetRun
	alt sdk.Remediation
}

func (u *unit) findings() []sdk.Finding { return u.tr.phases[u.tr.ph].findings }

// slot is the part of a batch that belongs to one group: some of the
// group's units of the round, on top of its accepted changes.
type slot struct {
	gr    *groupRun
	units []*unit
}

// unitResult is the outcome of verifying a unit.
type unitResult struct {
	manual *sdk.ManualChange
	v      verdict
}

func (tr *targetRun) unit() *unit {
	if tr.done {
		return nil
	}
	return &unit{tr: tr, alt: tr.phases[tr.ph].alternatives[tr.next]}
}

// settle records the outcome of the target's unit of the round, the way
// attempt does for one alternative.
func (e *Engine) settle(ctx context.Context, gr *groupRun, u *unit, r unitResult) {
	tr, ph, p := u.tr, u.tr.phases[u.tr.ph], gr.g.project
	switch {
	case r.manual != nil:
		tr.lastManual = r.manual
	case r.v.ok:
		e.progress(ctx, "change verified", "project", p.ID, "change", u.alt.Description)
		it := ph.item(u.alt)
		tr.item, tr.done = &it, true
		return
	default:
		tr.lastFailure = r.v.why
		if r.v.remaining && tr.candidate == nil {
			a := u.alt
			tr.candidate, tr.candidateWhy = &a, r.v.why
		}
		e.log.WarnContext(ctx, "change rejected", "project", p.ID, "change", u.alt.Description, "reason", r.v.why)
	}
	if tr.next++; tr.next < len(ph.alternatives) {
		return
	}
	if tr.candidate != nil && e.cfg.Fix.Unverified.Publish {
		e.log.WarnContext(ctx, "publishing unverified change", "project", p.ID, "change", tr.candidate.Description, "reason", tr.candidateWhy)
		it := ph.item(*tr.candidate)
		it.Unverified, it.VerifyFailure = true, tr.candidateWhy
		tr.item, tr.done = &it, true
		return
	}
	tr.manual, tr.failed = report(tr.manual, tr.failed, p.ID, ph.group, ph.first, tr.lastManual, tr.lastFailure)
	tr.ph, tr.next = tr.ph+1, 0
	tr.lastManual, tr.lastFailure, tr.candidate, tr.candidateWhy = nil, "", nil, ""
	tr.done = tr.ph >= len(tr.phases)
}

// fixBatches verifies the groups in batches, project by project in
// parallel, then assembles the change sets. Outcomes keep the order of the
// groups.
func (e *Engine) fixBatches(ctx context.Context, pool *fixPool, groups []*fixGroup) []groupOutcome {
	out := make([]groupOutcome, len(groups))
	byProject := map[string][]int{}
	var projects []string
	for i, g := range groups {
		if _, ok := byProject[g.project.ID]; !ok {
			projects = append(projects, g.project.ID)
		}
		byProject[g.project.ID] = append(byProject[g.project.ID], i)
	}
	var wg sync.WaitGroup
	for _, id := range projects {
		wg.Add(1)
		go func(idx []int) {
			defer wg.Done()
			e.fixProject(ctx, pool, groups, idx, out)
		}(byProject[id])
	}
	wg.Wait()
	return out
}

// fixProject runs the rounds of one project and assembles its change sets
// into out at the groups' indexes.
func (e *Engine) fixProject(ctx context.Context, pool *fixPool, groups []*fixGroup, idx []int, out []groupOutcome) {
	fail := func(err error) {
		for _, i := range idx {
			out[i] = groupOutcome{err: err}
		}
	}
	start := e.clock.Now()
	runs := make([]*groupRun, len(idx))
	for k, i := range idx {
		gr := &groupRun{g: groups[i]}
		for _, t := range groups[i].targets {
			gr.targets = append(gr.targets, &targetRun{phases: phases(groups[i], t)})
		}
		runs[k] = gr
	}
	p := groups[idx[0]].project
	for round := 1; ; round++ {
		var slots []slot
		for _, gr := range runs {
			s := slot{gr: gr}
			for _, tr := range gr.targets {
				if u := tr.unit(); u != nil {
					s.units = append(s.units, u)
				}
			}
			if len(s.units) > 0 {
				slots = append(slots, s)
			}
		}
		if len(slots) == 0 {
			break
		}
		batches := partition(slots)
		results := make([]map[*unit]unitResult, len(batches))
		errs := make([]error, len(batches))
		var wg sync.WaitGroup
		for b, batch := range batches {
			wg.Add(1)
			go pool.run(func(copies map[string]*scratch) {
				defer wg.Done()
				if errs[b] = ctx.Err(); errs[b] != nil {
					return
				}
				sc, err := pool.copyOf(copies, p)
				if err != nil {
					errs[b] = err
					return
				}
				changes := 0
				for _, s := range batch {
					changes += len(s.units)
				}
				e.progress(ctx, "verifying batch", "project", p.ID, "round", round, "batch", fmt.Sprintf("%d/%d", b+1, len(batches)),
					"groups", len(batch), "changes", changes)
				results[b] = map[*unit]unitResult{}
				_, errs[b] = e.verifySlots(ctx, sc, p, batch, results[b], true)
				if err := sc.reset(); err != nil && errs[b] == nil {
					errs[b] = err
				}
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				fail(err)
				return
			}
		}
		for b, batch := range batches {
			for _, s := range batch {
				for _, u := range s.units {
					e.settle(ctx, s.gr, u, results[b][u])
				}
			}
		}
	}
	var wg sync.WaitGroup
	for k, gr := range runs {
		i := idx[k]
		var items []FixItem
		for _, tr := range gr.targets {
			out[i].manual = append(out[i].manual, tr.manual...)
			out[i].failed = append(out[i].failed, tr.failed...)
			if tr.item != nil {
				items = append(items, *tr.item)
			}
		}
		if len(items) == 0 {
			e.groupDone(ctx, gr.g, nil, start)
			continue
		}
		wg.Add(1)
		go pool.run(func(copies map[string]*scratch) {
			defer wg.Done()
			sc, err := pool.copyOf(copies, p)
			if err != nil {
				out[i].err = err
				return
			}
			cs, f, err := e.assemble(ctx, sc, gr.g, items, true)
			if rerr := sc.reset(); rerr != nil && err == nil {
				err = rerr
			}
			out[i].cs, out[i].err = cs, err
			if f != nil {
				out[i].failed = append(out[i].failed, *f)
			}
			e.groupDone(ctx, gr.g, cs, start)
		})
	}
	wg.Wait()
}

// affects returns the packages a slot's state may alter: those of the
// group's accepted changes and of its units. ok is false when one of them
// declares none, so the slot needs a batch of its own.
func (s slot) affects() (map[string]bool, bool) {
	keys := map[string]bool{}
	add := func(r sdk.Remediation) bool {
		for _, k := range r.Affects {
			keys[strings.ToLower(k)] = true
		}
		return len(r.Affects) > 0
	}
	for _, tr := range s.gr.targets {
		if tr.item != nil && !add(tr.item.remedy) {
			return nil, false
		}
	}
	for _, u := range s.units {
		if !add(u.alt) {
			return nil, false
		}
	}
	return keys, true
}

// partition splits the slots of a round into batches greedily in group
// order: a slot joins the first batch none of whose packages it affects.
func partition(slots []slot) [][]slot {
	type bin struct {
		slots []slot
		keys  map[string]bool
		solo  bool
	}
	var bins []*bin
	for _, s := range slots {
		keys, ok := s.affects()
		if !ok {
			bins = append(bins, &bin{slots: []slot{s}, solo: true})
			continue
		}
		var target *bin
		for _, b := range bins {
			if b.solo || overlaps(b.keys, keys) {
				continue
			}
			target = b
			break
		}
		if target == nil {
			target = &bin{keys: map[string]bool{}}
			bins = append(bins, target)
		}
		target.slots = append(target.slots, s)
		for k := range keys {
			target.keys[k] = true
		}
	}
	out := make([][]slot, len(bins))
	for i, b := range bins {
		out[i] = b.slots
	}
	return out
}

func overlaps(a, b map[string]bool) bool {
	for k := range b {
		if a[k] {
			return true
		}
	}
	return false
}

func countUnits(slots []slot) int {
	n := 0
	for _, s := range slots {
		n += len(s.units)
	}
	return n
}

// verifySlots verifies a batch and records a result for every unit in out.
// It reports whether the batch as a whole built. When it does not, the
// batch is split in halves (by group, then by unit) down to the units that
// break it; when every half builds, the units are verified one by one.
func (e *Engine) verifySlots(ctx context.Context, sc *scratch, p sdk.Project, slots []slot, out map[*unit]unitResult, top bool) (bool, error) {
	why, res, err := e.check(ctx, sc, p, slots, top)
	if err != nil {
		return false, err
	}
	for u, r := range res {
		out[u] = r
	}
	if why == "" {
		return true, nil
	}
	// Units that could not be applied leave the batch.
	var left []slot
	for _, s := range slots {
		k := slot{gr: s.gr}
		for _, u := range s.units {
			if _, done := res[u]; !done {
				k.units = append(k.units, u)
			}
		}
		if len(k.units) > 0 {
			left = append(left, k)
		}
	}
	switch countUnits(left) {
	case 0:
		return false, nil
	case 1:
		out[left[0].units[0]] = unitResult{v: verdict{why: why}}
		return false, nil
	}
	var a, b []slot
	if len(left) > 1 {
		a, b = left[:len(left)/2], left[len(left)/2:]
	} else {
		s := left[0]
		a, b = []slot{{gr: s.gr, units: s.units[:len(s.units)/2]}}, []slot{{gr: s.gr, units: s.units[len(s.units)/2:]}}
	}
	okA, err := e.verifySlots(ctx, sc, p, a, out, false)
	if err != nil {
		return false, err
	}
	okB, err := e.verifySlots(ctx, sc, p, b, out, false)
	if err != nil {
		return false, err
	}
	if okA && okB {
		// The halves build but the whole does not: the changes only break
		// together, so verify them one by one, as fix.verify: each does.
		return false, e.oneByOne(ctx, sc, p, left, out)
	}
	return false, nil
}

// oneByOne verifies the units of every slot one after the other on top of
// the group's accepted changes and the units accepted before them.
func (e *Engine) oneByOne(ctx context.Context, sc *scratch, p sdk.Project, slots []slot, out map[*unit]unitResult) error {
	for _, s := range slots {
		var kept []*unit
		for _, u := range s.units {
			why, res, err := e.check(ctx, sc, p, []slot{{gr: s.gr, units: append(append([]*unit{}, kept...), u)}}, false)
			if err != nil {
				return err
			}
			r, ok := res[u]
			switch {
			case why != "" && !ok:
				r = unitResult{v: verdict{why: why}}
			case r.v.ok:
				kept = append(kept, u)
			}
			out[u] = r
		}
	}
	return nil
}

// check applies the slots to a restored copy in group and target order and
// rescans once. It returns why the project does not build ("" when it
// does) and the results it knows: units that could not be applied and,
// when the project builds, the verdict of every applied unit.
func (e *Engine) check(ctx context.Context, sc *scratch, p sdk.Project, slots []slot, log bool) (string, map[*unit]unitResult, error) {
	if err := sc.reset(); err != nil {
		return "", nil, err
	}
	res := map[*unit]unitResult{}
	var applied []*unit
	var findings []sdk.Finding
	for _, s := range slots {
		units := map[*targetRun]*unit{}
		for _, u := range s.units {
			units[u.tr] = u
		}
		for _, tr := range s.gr.targets {
			if tr.item != nil {
				// An accepted change: if it cannot be applied again, the
				// assembly reports it; verification goes on without it.
				if edits, m := e.realize(ctx, sc, p, tr.item.remedy); m == nil {
					if prev, err := sc.apply(edits); err != nil {
						sc.rollback(prev)
					}
				}
				continue
			}
			u := units[tr]
			if u == nil {
				continue
			}
			if log {
				e.progress(ctx, "verifying change", "project", p.ID, "change", u.alt.Description)
			}
			edits, m := e.realize(ctx, sc, p, u.alt)
			if m != nil {
				res[u] = unitResult{manual: m}
				continue
			}
			if prev, err := sc.apply(edits); err != nil {
				sc.rollback(prev)
				res[u] = unitResult{v: verdict{why: err.Error()}}
				continue
			}
			applied = append(applied, u)
			findings = append(findings, u.findings()...)
		}
	}
	if len(applied) == 0 {
		return "", res, nil
	}
	scan, err := e.rescan(ctx, sc, p, analyzersOf(findings))
	if err != nil {
		return "", nil, err
	}
	if why := buildFailure(scan, p); why != "" {
		return why, res, nil
	}
	for _, u := range applied {
		res[u] = unitResult{v: judge(scan, u.findings())}
	}
	return "", res, nil
}
