// Package docs runs the commands and validates the configuration examples
// found in docs/*.md, so documentation cannot drift from behavior.
package docs

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/deepward-oss/deepward/examples/hello-analyzer"
	"github.com/deepward-oss/deepward/internal/cli"
	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	_ "github.com/deepward-oss/deepward/plugins/all"
)

// Block is a fenced code block.
type Block struct {
	File string
	Line int
	Lang string
	Body string
}

var fence = regexp.MustCompile("^```([a-z]*)")

// Blocks extracts fenced code blocks from a markdown file.
func Blocks(t *testing.T, path string) []Block {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Block
	var cur *Block
	var body strings.Builder
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.HasPrefix(text, "```") {
			if cur == nil {
				m := fence.FindStringSubmatch(text)
				cur = &Block{File: filepath.Base(path), Line: line, Lang: m[1]}
				body.Reset()
			} else {
				cur.Body = body.String()
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		if cur != nil {
			body.WriteString(text)
			body.WriteByte('\n')
		}
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	return filepath.Join(dir, "..", "..")
}

type runner struct {
	t   *testing.T
	dir string
	env map[string]string
}

func newRunner(t *testing.T) *runner {
	t.Helper()
	dir := t.TempDir()
	r := &runner{t: t, dir: dir, env: map[string]string{}}
	// Files referenced by documented commands.
	r.write("build/bom.json", `{"bomFormat":"CycloneDX","specVersion":"1.5","components":[]}`)
	r.write("semgrep.sarif", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[]}]}`)
	r.write("ci/settings.xml", "<settings/>")
	return r
}

func (r *runner) write(rel, content string) {
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *runner) run(args []string) (int, string) {
	var out, errb bytes.Buffer
	code := cli.Run(context.Background(), cli.Env{
		Args: args, Stdout: &out, Stderr: &errb, Cwd: r.dir, Registry: sdk.DefaultRegistry(),
		Getenv: func(k string) string { return r.env[k] }, Clock: sdk.FixedClock{T: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)},
		NewTools: toolsFactory,
	})
	return code, out.String() + errb.String()
}

var toolsFactory = cli.DefaultTools

func TestDocumentedCommandsRun(t *testing.T) {
	for _, doc := range []string{"cli.md"} {
		for _, b := range Blocks(t, filepath.Join(repoRoot(t), "docs", doc)) {
			if b.Lang != "sh" {
				continue
			}
			r := newRunner(t)
			// A report to render for `deepward report --from ...`.
			if code, out := r.run([]string{"scan", "--format", "json,html", "--allow-partial", "--quiet"}); code != 0 {
				t.Fatalf("seed scan failed (%d): %s", code, out)
			}
			for i, line := range strings.Split(strings.TrimSpace(b.Body), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 || fields[0] != "deepward" {
					t.Errorf("%s:%d: command does not start with deepward: %s", b.File, b.Line+i+1, line)
					continue
				}
				if code, out := r.run(fields[1:]); code != 0 {
					t.Errorf("%s:%d: %q exited %d:\n%s", b.File, b.Line+i+1, line, code, out)
				}
			}
		}
	}
}

func TestDocumentedConfigurationIsValid(t *testing.T) {
	docs, _ := filepath.Glob(filepath.Join(repoRoot(t), "docs", "*.md"))
	more, _ := filepath.Glob(filepath.Join(repoRoot(t), "docs", "*", "*.md"))
	docs = append(docs, more...)
	checked := 0
	for _, doc := range docs {
		for _, b := range Blocks(t, doc) {
			if b.Lang != "yaml" {
				continue
			}
			r := newRunner(t)
			r.env["NEXUS_TOKEN"] = "x"
			r.write("deepward.yaml", b.Body)
			if code, out := r.run([]string{"config", "validate"}); code != 0 {
				t.Errorf("%s:%d: configuration example is invalid (%d):\n%s", b.File, b.Line, code, out)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no YAML examples found")
	}
}

// TestEveryAdvisorySourceIsListed is task 12.4: every registered advisory
// source must appear in docs/data-licenses.md with its license.
func TestEveryAdvisorySourceIsListed(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "data-licenses.md"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, p := range sdk.DefaultRegistry().ByKind(deps.KindAdvisorySource) {
		src := p.(deps.AdvisorySource)
		row := "| `" + p.Info().ID + "` |"
		_, line, ok := strings.Cut(string(doc), row)
		if !ok {
			t.Errorf("advisory source %s is not listed in docs/data-licenses.md", p.Info().ID)
			continue
		}
		line, _, _ = strings.Cut(line, "\n")
		if !strings.Contains(line, src.DataLicense().SPDX) {
			t.Errorf("%s: documented license differs from %s", p.Info().ID, src.DataLicense().SPDX)
		}
		found++
	}
	if found == 0 {
		t.Fatal("no advisory sources registered")
	}
}
