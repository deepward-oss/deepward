package engine

import (
	"bytes"
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

	"github.com/deepward-oss/deepward/internal/textedit"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// scratch is a private copy of the files a project needs. It tracks the
// content of every file Deepward knows about (state), so edits, post tasks
// and rollbacks can add, change and delete files.
type scratch struct {
	ws       *sdk.Workspace
	original map[string][]byte
	// state is the current content of the copied files and of files created
	// since; a missing key is a file that does not exist.
	state map[string][]byte
	// touched lists the files changed since the copy was made or reset.
	touched map[string]bool
	// cache holds verification scans by project, file contents and analyzers.
	cache map[string]*sdk.ScanResult
}

// fileState is a file's content before a change, for rollback.
type fileState struct {
	data   []byte
	exists bool
}

// snapshot holds the previous state of the files a change touched.
type snapshot map[string]fileState

func (e *Engine) newScratch(p sdk.Project) (*scratch, error) {
	dir, err := os.MkdirTemp("", "deepward-fix-")
	if err != nil {
		return nil, err
	}
	sc := &scratch{ws: sdk.NewWorkspace(dir, e.ws.Exclude), original: map[string][]byte{}, state: map[string][]byte{},
		touched: map[string]bool{}, cache: map[string]*sdk.ScanResult{}}
	copyFile := func(rel string) error {
		data, err := fs.ReadFile(e.ws.FS, rel)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		sc.original[rel], sc.state[rel] = data, data
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

func (s *scratch) abs(rel string) string { return filepath.Join(s.ws.Root, filepath.FromSlash(rel)) }

func (s *scratch) read(rel string) ([]byte, error) { return os.ReadFile(s.abs(rel)) }

// record keeps the state of rel before its first change in prev.
func (s *scratch) record(prev snapshot, rel string) {
	if prev == nil {
		return
	}
	if _, done := prev[rel]; done {
		return
	}
	data, ok := s.state[rel]
	prev[rel] = fileState{data: data, exists: ok}
}

// set writes rel and records its previous state in prev (if not nil).
func (s *scratch) set(prev snapshot, rel string, data []byte) error {
	s.record(prev, rel)
	dst := s.abs(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	s.state[rel], s.touched[rel] = data, true
	return os.WriteFile(dst, data, 0o644)
}

// remove deletes rel and records its previous state in prev (if not nil).
func (s *scratch) remove(prev snapshot, rel string) error {
	s.record(prev, rel)
	delete(s.state, rel)
	s.touched[rel] = true
	if err := os.Remove(s.abs(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// apply applies edits and returns the previous state for rollback.
func (s *scratch) apply(edits []sdk.TextEdit) (snapshot, error) {
	prev := snapshot{}
	for file, es := range textedit.ByFile(edits) {
		cur, err := s.read(file)
		if err != nil {
			return prev, err
		}
		next, err := textedit.Apply(cur, es)
		if err != nil {
			return prev, err
		}
		if err := s.set(prev, file, next); err != nil {
			return prev, err
		}
	}
	return prev, nil
}

// rollback restores the files a change touched.
func (s *scratch) rollback(prev snapshot) {
	for file, st := range prev {
		if st.exists {
			_ = s.set(nil, file, st.data)
		} else {
			_ = s.remove(nil, file)
		}
	}
}

// reset restores every file changed since the copy was made, so the copy can
// serve the next group of the same project.
func (s *scratch) reset() error {
	for f := range s.touched {
		var err error
		if data, ok := s.original[f]; ok {
			err = s.set(nil, f, data)
		} else {
			err = s.remove(nil, f)
		}
		if err != nil {
			return fmt.Errorf("restore %s: %w", f, err)
		}
	}
	s.touched = map[string]bool{}
	return nil
}

// sync takes the changes a command made in the copy: changes to files that
// match keep (all files when keep is empty) become part of the state and are
// recorded in prev; other changes are undone. Excluded directories (build
// output such as target/) are ignored.
func (s *scratch) sync(prev snapshot, keep []*regexp.Regexp) error {
	kept := func(rel string) bool {
		if len(keep) == 0 {
			return true
		}
		for _, re := range keep {
			if re.MatchString(rel) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	err := fs.WalkDir(os.DirFS(s.ws.Root), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != "." && s.ws.IsExcluded(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		seen[rel] = true
		data, err := s.read(rel)
		if err != nil {
			return err
		}
		cur, known := s.state[rel]
		switch {
		case known && bytes.Equal(cur, data):
		case kept(rel):
			return s.set(prev, rel, data)
		case known:
			return os.WriteFile(s.abs(rel), cur, 0o644)
		default:
			return os.Remove(s.abs(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for rel, data := range s.state {
		if seen[rel] || s.underExcluded(rel) {
			continue
		}
		if kept(rel) {
			if err := s.remove(prev, rel); err != nil {
				return err
			}
			continue
		}
		if err := s.set(nil, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// underExcluded reports whether rel is inside an excluded directory (such
// files are copied manifests the walk does not visit).
func (s *scratch) underExcluded(rel string) bool {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if s.ws.IsExcluded(dir) {
			return true
		}
	}
	return false
}

func (s *scratch) hash() string {
	h := sha256.New()
	files := make([]string, 0, len(s.state))
	for f := range s.state {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(s.state[f])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// changes returns the files that differ from the original copy and their
// unified diff.
func (s *scratch) changes() ([]sdk.FileChange, string) {
	files := make([]string, 0, len(s.touched))
	for f := range s.touched {
		files = append(files, f)
	}
	sort.Strings(files)
	var out []sdk.FileChange
	var diff bytes.Buffer
	for _, f := range files {
		before, existed := s.original[f]
		after, exists := s.state[f]
		switch {
		case exists && (!existed || !bytes.Equal(before, after)):
			out = append(out, sdk.FileChange{Path: f, Content: after})
			diff.WriteString(textedit.Unified(f, before, after))
		case !exists && existed:
			out = append(out, sdk.FileChange{Path: f, Delete: true})
			diff.WriteString(textedit.Unified(f, before, nil))
		}
	}
	return out, diff.String()
}
