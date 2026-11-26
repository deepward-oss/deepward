package sdk

import (
	"io/fs"
	"os"
	"path"
	"strings"
)

// DefaultExcludedDirs are never descended into during detection.
var DefaultExcludedDirs = []string{".git", "target", "bin", "obj", "node_modules", "deepward-out", ".deepward-cache", ".bsp", ".idea"}

// Workspace is the scan root.
type Workspace struct {
	// Root is the absolute path of the scan root.
	Root string
	// FS reads files relative to Root (slash-separated paths).
	FS fs.FS
	// Exclude holds extra slash-separated directory paths (relative to Root)
	// or glob patterns (path.Match syntax) to skip.
	Exclude []string
}

// NewWorkspace returns a workspace backed by the OS file system.
func NewWorkspace(root string, exclude []string) *Workspace {
	return &Workspace{Root: root, FS: os.DirFS(root), Exclude: exclude}
}

// IsExcluded reports whether a directory (relative, slash-separated) is skipped.
func (w *Workspace) IsExcluded(rel string) bool {
	rel = strings.Trim(path.Clean(rel), "/")
	if rel == "." || rel == "" {
		return false
	}
	base := path.Base(rel)
	for _, d := range DefaultExcludedDirs {
		if base == d {
			return true
		}
	}
	for _, pattern := range w.Exclude {
		pattern = strings.Trim(path.Clean(pattern), "/")
		if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
	}
	return false
}

// Walk visits every non-excluded file and directory under Root in lexical order.
func (w *Workspace) Walk(fn func(rel string, d fs.DirEntry) error) error {
	return fs.WalkDir(w.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && w.IsExcluded(p) {
			return fs.SkipDir
		}
		return fn(p, d)
	})
}

// Project is a unit of analysis found by an ecosystem plugin.
type Project struct {
	// ID is stable across runs: "<ecosystem>:<dir>", e.g. "maven:services/api".
	ID string `json:"id"`
	// Ecosystem is the ID of the ecosystem plugin that owns the project.
	Ecosystem string `json:"ecosystem"`
	// Dir is the project directory relative to the workspace root ("." for root).
	Dir string `json:"dir"`
	// Manifests are the files that declare the project's dependencies or build.
	Manifests []string `json:"manifests"`
	// Attributes carries ecosystem-specific details (e.g. modules).
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ProjectID builds the canonical project ID.
func ProjectID(ecosystem, dir string) string {
	dir = path.Clean(strings.ReplaceAll(dir, "\\", "/"))
	return ecosystem + ":" + dir
}
