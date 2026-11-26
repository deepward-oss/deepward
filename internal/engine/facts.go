package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

type factEntry struct {
	done     chan struct{}
	val      any
	err      error
	producer string
}

// projectFacts implements sdk.Facts for one project: each fact is produced
// at most once and shared by all callers.
type projectFacts struct {
	e       *Engine
	ws      *sdk.Workspace
	project sdk.Project
	mu      sync.Mutex
	entries map[string]*factEntry
	runs    *runLog
}

func newProjectFacts(e *Engine, ws *sdk.Workspace, p sdk.Project, runs *runLog) *projectFacts {
	return &projectFacts{e: e, ws: ws, project: p, entries: map[string]*factEntry{}, runs: runs}
}

type inProgressKey struct{}

// Fact implements sdk.Facts.
func (pf *projectFacts) Fact(ctx context.Context, name string) (any, error) {
	chain, _ := ctx.Value(inProgressKey{}).(map[string]bool)
	if chain[name] {
		return nil, &sdk.FactError{Name: name, Reason: "dependency cycle between fact producers"}
	}
	pf.mu.Lock()
	entry, ok := pf.entries[name]
	if !ok {
		entry = &factEntry{done: make(chan struct{})}
		pf.entries[name] = entry
	}
	pf.mu.Unlock()
	if ok {
		select {
		case <-entry.done:
			return entry.val, entry.err
		case <-ctx.Done():
			return nil, &sdk.FactError{Name: name, Err: ctx.Err()}
		}
	}
	next := map[string]bool{name: true}
	for k := range chain {
		next[k] = true
	}
	val, producer, err := pf.produce(context.WithValue(ctx, inProgressKey{}, next), name)
	entry.val, entry.producer = val, producer
	if err != nil {
		var fe *sdk.FactError
		if !errors.As(err, &fe) {
			err = &sdk.FactError{Name: name, Err: err}
		}
		entry.err = err
	}
	close(entry.done)
	return entry.val, entry.err
}

func (pf *projectFacts) produce(ctx context.Context, name string) (any, string, error) {
	if name == sdk.FactSourceTree {
		t, err := sdk.NewSourceTree(pf.ws, pf.project.Dir)
		return t, "engine", err
	}
	var producer sdk.FactProducer
	for _, p := range pf.e.Enabled(sdk.KindFactProducer) {
		fp, ok := p.(sdk.FactProducer)
		if ok && produces(fp, name) && fp.AppliesTo(pf.project) {
			producer = fp
			break
		}
	}
	if producer == nil {
		if eco, ok := pf.e.enabled[pf.project.Ecosystem].(sdk.FactProducer); ok && produces(eco, name) {
			producer = eco
		}
	}
	if producer == nil {
		return nil, "", &sdk.FactError{Name: name, Reason: "no producer for this project"}
	}
	id := producer.Info().ID
	pf.e.progress(ctx, "producing fact", "fact", name, "project", pf.project.ID, "plugin", id)
	start := pf.e.clock.Now()
	var val any
	err := guard(ctx, pf.e.pluginTimeout(), func(ctx context.Context) error {
		v, err := producer.Produce(ctx, sdk.FactRequest{Name: name, Project: pf.project, Workspace: pf.ws, Facts: pf})
		val = v
		return err
	})
	run := sdk.PluginRun{Plugin: id, Kind: producer.Info().Kind, Project: pf.project.ID, Status: sdk.RunOK}
	if err != nil {
		run.Status, run.Reason = sdk.RunFailed, err.Error()
		var te *sdk.ToolError
		if errors.As(err, &te) {
			run.Status = sdk.RunSkipped
		}
	} else if val == nil {
		err = fmt.Errorf("producer %s returned no value", id)
		run.Status, run.Reason = sdk.RunFailed, err.Error()
	}
	pf.runs.add(run)
	pf.e.progress(ctx, "fact ready", "fact", name, "project", pf.project.ID, "status", run.Status, "duration", pf.e.since(start))
	return val, id, err
}

func produces(p sdk.FactProducer, name string) bool {
	for _, n := range p.Produces() {
		if n == name {
			return true
		}
	}
	return false
}

// snapshot returns the finished entries.
func (pf *projectFacts) snapshot() map[string]*factEntry {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	out := make(map[string]*factEntry, len(pf.entries))
	for k, v := range pf.entries {
		select {
		case <-v.done:
			out[k] = v
		default:
		}
	}
	return out
}

type runLog struct {
	mu   sync.Mutex
	runs []sdk.PluginRun
	warn []sdk.Warning
}

func (l *runLog) add(r sdk.PluginRun) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.runs = append(l.runs, r)
	l.mu.Unlock()
}

func (l *runLog) warning(w sdk.Warning) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.warn = append(l.warn, w)
	l.mu.Unlock()
}
