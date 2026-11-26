package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"sync"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// projectState tracks one project during a run.
type projectState struct {
	project sdk.Project
	facts   *projectFacts
	// setupErr is set when a setup step failed.
	setupErr  error
	setupTail []string
}

// Detect runs every enabled ecosystem and returns the projects, sorted by ID.
// runs may be nil.
func (e *Engine) Detect(ctx context.Context, ws *sdk.Workspace, runs *runLog) []sdk.Project {
	var out []sdk.Project
	seen := map[string]bool{}
	for _, p := range e.Enabled(sdk.KindEcosystem) {
		eco, ok := p.(sdk.Ecosystem)
		if !ok {
			continue
		}
		var projects []sdk.Project
		err := guard(ctx, e.pluginTimeout(), func(ctx context.Context) error {
			var err error
			projects, err = eco.Detect(ctx, ws)
			return err
		})
		run := sdk.PluginRun{Plugin: p.Info().ID, Kind: sdk.KindEcosystem, Status: sdk.RunOK, Findings: 0}
		if err != nil {
			run.Status, run.Reason = sdk.RunFailed, err.Error()
			runs.warning(sdk.Warning{Code: "plugin-failed", Plugin: p.Info().ID, Message: "project detection failed: " + err.Error()})
		}
		runs.add(run)
		for _, pr := range projects {
			if seen[pr.ID] {
				continue
			}
			seen[pr.ID] = true
			out = append(out, pr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Scan runs the scan pipeline on the engine's workspace.
func (e *Engine) Scan(ctx context.Context) (*sdk.ScanResult, error) {
	if e.ws == nil {
		return nil, errors.New("engine has no workspace")
	}
	return e.ScanWorkspace(ctx, e.ws, ScanFilter{})
}

// ScanFilter restricts a scan (used by the fix pipeline for verification).
type ScanFilter struct {
	// Projects limits the scan to these project IDs (all when empty).
	Projects []string
	// Analyzers limits the scan to these analyzer IDs (all when empty).
	Analyzers []string
}

func (f ScanFilter) project(id string) bool { return len(f.Projects) == 0 || contains(f.Projects, id) }
func (f ScanFilter) analyzer(id string) bool {
	return len(f.Analyzers) == 0 || contains(f.Analyzers, id)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ScanWorkspace runs the scan pipeline on ws.
func (e *Engine) ScanWorkspace(ctx context.Context, ws *sdk.Workspace, filter ScanFilter) (*sdk.ScanResult, error) {
	started := e.clock.Now()
	runs := &runLog{}
	projects := e.Detect(ctx, ws, runs)
	e.progress(ctx, "projects detected", "count", len(projects))
	var states []*projectState
	for _, p := range projects {
		if !filter.project(p.ID) {
			continue
		}
		st := &projectState{project: p, facts: newProjectFacts(e, ws, p, runs)}
		e.runSetup(ctx, ws, st)
		states = append(states, st)
	}
	findings := e.runAnalyzers(ctx, ws, states, filter, runs)
	e.progress(ctx, "scan finished", "projects", len(states), "findings", len(findings), "duration", e.since(started))
	res := &sdk.ScanResult{
		SchemaVersion: sdk.SchemaVersion,
		Tool:          e.opts.Tool,
		Run:           e.opts.Run,
		Findings:      findings,
	}
	res.Tool.SDKVersion = sdk.SDKVersion
	res.Run.StartedAt = started
	res.Run.Offline = e.cfg.Offline
	if res.Run.Root == "" {
		res.Run.Root = filepath.ToSlash(ws.Root)
	}
	for _, st := range states {
		res.Projects = append(res.Projects, e.projectResult(st, runs))
	}
	for _, w := range applyIgnoreRules(res.Findings, e.cfg.Ignore, e.clock.Now()) {
		runs.warning(w)
	}
	for _, p := range e.enabledPlugins() {
		if ds, ok := p.(sdk.DataSourceReporter); ok {
			res.DataSources = append(res.DataSources, ds.DataSources()...)
		}
		if wr, ok := p.(sdk.WarningReporter); ok {
			for _, w := range wr.Warnings() {
				if w.Plugin == "" {
					w.Plugin = p.Info().ID
				}
				runs.warning(w)
			}
		}
	}
	res.PluginRuns = runs.runs
	res.Warnings = runs.warn
	res.Gate = evaluateGate(res.Findings, e.cfg.FailOn)
	res.Run.FinishedAt = e.clock.Now()
	res.Normalize()
	return res, nil
}

func (e *Engine) enabledPlugins() []sdk.Plugin {
	out := make([]sdk.Plugin, 0, len(e.enabled))
	for _, id := range e.enabledIDs() {
		out = append(out, e.enabled[id])
	}
	return out
}

func (e *Engine) runSetup(ctx context.Context, ws *sdk.Workspace, st *projectState) {
	steps := append([]SetupStep{}, e.cfg.Ecosystems[st.project.Ecosystem].Setup...)
	steps = append(steps, e.cfg.Projects[st.project.Dir].Setup...)
	for _, s := range steps {
		tool, err := e.tools.Require(ctx, sdk.ToolRequirement{Name: s.Tool, Purpose: "setup step"}, &st.project)
		if err != nil {
			st.setupErr = fmt.Errorf("setup step %s: %w", s.Tool, err)
			return
		}
		dir := filepath.Join(ws.Root, filepath.FromSlash(path.Join(st.project.Dir, s.Dir)))
		res, err := tool.Run(ctx, sdk.RunSpec{Args: s.Args, Dir: dir, Env: s.Env})
		if err != nil {
			st.setupErr = fmt.Errorf("setup step %s %v: %w", s.Tool, s.Args, err)
			st.setupTail = res.Tail
			return
		}
	}
}

type task struct {
	analyzer sdk.Analyzer
	state    *projectState // nil for workspace analyzers
}

func (e *Engine) runAnalyzers(ctx context.Context, ws *sdk.Workspace, states []*projectState, filter ScanFilter, runs *runLog) []sdk.Finding {
	var tasks []task
	var projects []sdk.Project
	for _, st := range states {
		projects = append(projects, st.project)
	}
	for _, p := range e.Enabled(sdk.KindAnalyzer) {
		a, ok := p.(sdk.Analyzer)
		if !ok || !filter.analyzer(p.Info().ID) {
			continue
		}
		if a.Scope() == sdk.ScopeWorkspace {
			tasks = append(tasks, task{analyzer: a})
			continue
		}
		for _, st := range states {
			tasks = append(tasks, task{analyzer: a, state: st})
		}
	}
	conc := e.cfg.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	var all []sdk.Finding
	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(t task) {
			defer wg.Done()
			defer func() { <-sem }()
			fs := e.runTask(ctx, ws, t, projects, runs)
			mu.Lock()
			all = append(all, fs...)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return all
}

func (e *Engine) runTask(ctx context.Context, ws *sdk.Workspace, t task, projects []sdk.Project, runs *runLog) []sdk.Finding {
	id := t.analyzer.Info().ID
	run := sdk.PluginRun{Plugin: id, Kind: sdk.KindAnalyzer, Status: sdk.RunOK}
	req := sdk.AnalyzeRequest{Workspace: ws, Projects: projects}
	if t.state != nil {
		p := t.state.project
		run.Project = p.ID
		req.Project, req.Facts = &p, t.state.facts
		if t.state.setupErr != nil {
			run.Status, run.Reason = sdk.RunSkipped, "setup-failed"
			runs.add(run)
			return nil
		}
		for _, name := range t.analyzer.Requires() {
			if _, err := t.state.facts.Fact(ctx, name); err != nil {
				run.Status, run.Reason = sdk.RunSkipped, "fact-unavailable: "+name
				runs.add(run)
				return nil
			}
		}
	}
	var found []sdk.Finding
	start := e.clock.Now()
	defer func() {
		e.progress(ctx, "analyzer finished", "analyzer", id, "project", run.Project, "status", run.Status, "findings", len(found), "duration", e.since(start))
	}()
	err := guard(ctx, e.pluginTimeout(), func(ctx context.Context) error {
		fs, err := t.analyzer.Analyze(ctx, req)
		found = fs
		return err
	})
	if err != nil {
		run.Status, run.Reason = sdk.RunFailed, err.Error()
		runs.add(run)
		w := sdk.Warning{Code: "plugin-failed", Plugin: id, Message: fmt.Sprintf("analyzer %s failed: %v", id, err)}
		if t.state != nil {
			w.Project = t.state.project.ID
		}
		runs.warning(w)
		return nil
	}
	valid := found[:0]
	for _, f := range found {
		f.Analyzer = id
		if f.Project == "" && t.state != nil {
			f.Project = t.state.project.ID
		}
		if verr := f.Validate(); verr != nil {
			runs.warning(sdk.Warning{Code: "invalid-finding", Plugin: id, Project: f.Project, Message: verr.Error()})
			continue
		}
		valid = append(valid, f)
	}
	run.Findings = len(valid)
	runs.add(run)
	return valid
}

func (e *Engine) projectResult(st *projectState, runs *runLog) sdk.ProjectResult {
	pr := sdk.ProjectResult{Project: st.project, Status: sdk.ProjectOK}
	if st.setupErr != nil {
		pr.Status, pr.Reason, pr.Output = sdk.ProjectFailed, st.setupErr.Error(), st.setupTail
		runs.warning(sdk.Warning{Code: "setup-failed", Project: st.project.ID, Message: st.setupErr.Error()})
		return pr
	}
	entries := st.facts.snapshot()
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := entries[name]
		if entry.err == nil {
			if rf, ok := entry.val.(sdk.ReportableFact); ok {
				if pr.Facts == nil {
					pr.Facts = map[string]any{}
				}
				pr.Facts[name] = rf.ReportFact()
			}
			continue
		}
		var te *sdk.ToolError
		var xe *sdk.ExitError
		code, status, output := "project-failed", sdk.ProjectFailed, []string(nil)
		switch {
		case errors.As(entry.err, &te):
			code, status = "project-skipped", sdk.ProjectSkipped
		case errors.As(entry.err, &xe):
			output = xe.Tail
		}
		runs.warning(sdk.Warning{Code: code, Project: st.project.ID, Plugin: entry.producer, Message: entry.err.Error()})
		if pr.Status == sdk.ProjectOK {
			pr.Status, pr.Reason, pr.Output = status, entry.err.Error(), output
			if te != nil {
				pr.Reason = te.Error()
			}
		}
	}
	return pr
}

// Failed reports whether any project failed (for the partial-failure exit code).
func Failed(res *sdk.ScanResult) bool {
	for _, p := range res.Projects {
		if p.Status == sdk.ProjectFailed {
			return true
		}
	}
	for _, r := range res.PluginRuns {
		if r.Status == sdk.RunFailed && r.Kind == sdk.KindAnalyzer {
			return true
		}
	}
	return false
}
