package deps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Regenerate lets an ecosystem realize a change with its package manager.
// It snapshots files (slash-separated, relative to root), runs fn, which may
// edit them and run tools in root, and returns full-file text edits for the
// files that changed. The files are then restored, so the caller's workspace
// is unchanged and the engine applies the edits as usual. Files listed but
// missing before fn ran are removed again if fn created them.
func Regenerate(ctx context.Context, root string, files []string, fn func(ctx context.Context) error) ([]sdk.TextEdit, error) {
	type snapshot struct {
		data   []byte
		exists bool
	}
	before := make(map[string]snapshot, len(files))
	var order []string
	for _, rel := range files {
		if _, dup := before[rel]; dup {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case err == nil:
			before[rel] = snapshot{data: data, exists: true}
		case errors.Is(err, fs.ErrNotExist):
			before[rel] = snapshot{}
		default:
			return nil, err
		}
		order = append(order, rel)
	}
	restore := func() {
		for rel, s := range before {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if s.exists {
				_ = os.WriteFile(p, s.data, 0o644)
			} else {
				_ = os.Remove(p)
			}
		}
	}
	defer restore()
	if err := fn(ctx); err != nil {
		return nil, err
	}
	var edits []sdk.TextEdit
	for _, rel := range order {
		s := before[rel]
		if !s.exists {
			continue
		}
		after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s disappeared while updating: %w", rel, err)
		}
		if bytes.Equal(after, s.data) {
			continue
		}
		edits = append(edits, sdk.TextEdit{Path: rel, Start: 0, End: len(s.data), Replacement: string(after), Expected: string(s.data)})
	}
	return edits, nil
}

// WriteFile writes a file inside root (a slash-separated relative path); for
// use inside Regenerate callbacks.
func WriteFile(root, rel string, data []byte) error {
	return os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644)
}

// ReadFile reads a file inside root (a slash-separated relative path).
func ReadFile(root, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
}
