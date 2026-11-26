package sdk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// FactKey is a typed fact name.
type FactKey[T any] struct{ Name string }

// NewFactKey declares a typed fact key.
func NewFactKey[T any](name string) FactKey[T] { return FactKey[T]{Name: name} }

// Facts gives read access to the facts of one project. Facts are produced
// lazily, at most once per project, and shared between plugins.
type Facts interface {
	Fact(ctx context.Context, name string) (any, error)
}

// ErrFactUnavailable is wrapped by errors for facts that could not be produced.
var ErrFactUnavailable = errors.New("fact-unavailable")

// FactError explains why a fact is unavailable.
type FactError struct {
	Name   string
	Reason string
	Err    error
}

func (e *FactError) Error() string {
	msg := fmt.Sprintf("fact-unavailable: %s", e.Name)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Is makes errors.Is(err, ErrFactUnavailable) true.
func (e *FactError) Is(target error) bool { return target == ErrFactUnavailable }

// Unwrap returns the underlying error.
func (e *FactError) Unwrap() error { return e.Err }

// GetFact reads a typed fact.
func GetFact[T any](ctx context.Context, facts Facts, key FactKey[T]) (T, error) {
	var zero T
	if facts == nil {
		return zero, &FactError{Name: key.Name, Reason: "no facts in this scope"}
	}
	v, err := facts.Fact(ctx, key.Name)
	if err != nil {
		return zero, err
	}
	t, ok := v.(T)
	if !ok {
		return zero, &FactError{Name: key.Name, Reason: fmt.Sprintf("unexpected type %T", v)}
	}
	return t, nil
}

// ReportableFact is implemented by fact values that should be embedded in
// the scan result (e.g. the dependency graph).
type ReportableFact interface {
	ReportFact() any
}

// FactSourceTree is the built-in fact listing a project's files.
const FactSourceTree = "source-tree"

// SourceTreeKey is the typed key of the source-tree fact.
var SourceTreeKey = NewFactKey[*SourceTree](FactSourceTree)

// SourceTree is a read-only view of a project's files.
type SourceTree struct {
	Workspace *Workspace
	Dir       string
	// Files are slash-separated paths relative to the workspace root, sorted.
	Files []string
}

// NewSourceTree lists the non-excluded files below dir.
func NewSourceTree(ws *Workspace, dir string) (*SourceTree, error) {
	dir = path.Clean(dir)
	t := &SourceTree{Workspace: ws, Dir: dir}
	err := fs.WalkDir(ws.FS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && ws.IsExcluded(p) {
				return fs.SkipDir
			}
			return nil
		}
		t.Files = append(t.Files, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(t.Files)
	return t, nil
}
