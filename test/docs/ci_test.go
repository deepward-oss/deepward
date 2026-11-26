package docs

import (
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCISnippetsLint is task 15.5: the GitLab CI and GitHub Actions samples
// in the docs are valid YAML with the structure their platform expects.
func TestCISnippetsLint(t *testing.T) {
	checked := 0
	for _, b := range Blocks(t, filepath.Join(repoRoot(t), "docs", "publishing.md")) {
		if b.Lang != "yml" {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(b.Body), &doc); err != nil {
			t.Errorf("%s:%d: invalid YAML: %v", b.File, b.Line, err)
			continue
		}
		checked++
		if jobs, ok := doc["jobs"].(map[string]any); ok {
			if _, ok := doc["on"]; !ok {
				t.Errorf("%s:%d: GitHub workflow without 'on'", b.File, b.Line)
			}
			for name, j := range jobs {
				job, _ := j.(map[string]any)
				if job["runs-on"] == nil || job["steps"] == nil {
					t.Errorf("%s:%d: job %s needs runs-on and steps", b.File, b.Line, name)
				}
			}
			continue
		}
		for name, j := range doc {
			job, ok := j.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := job["script"].([]any); !ok {
				t.Errorf("%s:%d: GitLab job %s needs a script list", b.File, b.Line, name)
			}
		}
	}
	if checked != 2 {
		t.Errorf("expected 2 CI snippets, found %d", checked)
	}
}
