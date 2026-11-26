package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

func newScanCmd(env Env, g *globalOpts) *cobra.Command {
	r := &runFlags{}
	cmd := &cobra.Command{
		Use:   "scan [root]",
		Short: "Analyze the repository and write reports",
		Long: "Detect projects, build their dependency graphs with the native build tools, run all enabled analyzers\n" +
			"and write the reports (HTML and JSON by default) to the output directory.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := prepare(cmd.Context(), env, g, r, args)
			if err != nil {
				return err
			}
			return s.scan(cmd.Context())
		},
	}
	r.register(cmd, true)
	return cmd
}

func (s *session) reporterFormats() []string {
	var out []string
	for _, p := range s.eng.Enabled(sdk.KindReporter) {
		if rep, ok := p.(sdk.Reporter); ok {
			out = append(out, rep.Format())
		}
	}
	sort.Strings(out)
	return out
}

func (s *session) scan(ctx context.Context) error {
	formats := s.cfg.Output.Formats
	if len(formats) == 0 {
		formats = DefaultFormats
	}
	if err := validFormats(formats, s.reporterFormats()); err != nil {
		return err
	}
	res, err := s.eng.Scan(ctx)
	if err != nil {
		return exitCode(ExitRuntime, err)
	}
	outDir := s.outDir()
	written, err := writeReports(ctx, s.eng, res, formats, outDir, s.root)
	if err != nil {
		return exitCode(ExitRuntime, err)
	}
	if !s.g.quiet {
		printSummary(s.env.Stdout, res, written, colorsEnabled(s.env, s.g))
	}
	return exitCode(resultExitCode(res, s.cfg.AllowPartial), nil)
}

func (s *session) outDir() string {
	dir := firstNonEmpty(s.cfg.Output.Dir, DefaultOutputDir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.root, dir)
	}
	return dir
}

func resultExitCode(res *sdk.ScanResult, allowPartial bool) int {
	if !allowPartial && engine.Failed(res) {
		return ExitRuntime
	}
	if res.Gate.Failed {
		return ExitGate
	}
	return ExitOK
}

func writeReports(ctx context.Context, eng *engine.Engine, res *sdk.ScanResult, formats []string, outDir, root string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	var written []string
	for _, f := range formats {
		rep, _ := eng.Reporter(f)
		var buf bytes.Buffer
		if err := eng.Render(ctx, res, f, &buf); err != nil {
			return written, fmt.Errorf("render %s report: %w", f, err)
		}
		path := filepath.Join(outDir, rep.FileName())
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		written = append(written, filepath.ToSlash(path))
	}
	return written, nil
}

type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) severity(sev sdk.Severity, s string) string {
	switch sev {
	case sdk.SeverityCritical:
		return p.wrap("1;35", s)
	case sdk.SeverityHigh:
		return p.wrap("1;31", s)
	case sdk.SeverityMedium:
		return p.wrap("33", s)
	case sdk.SeverityLow:
		return p.wrap("36", s)
	}
	return s
}

func printSummary(w io.Writer, res *sdk.ScanResult, written []string, colors bool) {
	p := palette{on: colors}
	counts := map[sdk.ProjectStatus]int{}
	for _, pr := range res.Projects {
		counts[pr.Status]++
	}
	fmt.Fprintf(w, "Deepward %s: %d project(s) scanned (%d ok, %d skipped, %d failed)\n",
		res.Tool.Version, len(res.Projects), counts[sdk.ProjectOK], counts[sdk.ProjectSkipped], counts[sdk.ProjectFailed])
	for _, pr := range res.Projects {
		if pr.Status != sdk.ProjectOK {
			fmt.Fprintf(w, "  %s %s: %s\n", p.wrap("33", string(pr.Status)), pr.ID, pr.Reason)
		}
	}
	var parts []string
	built := res.Summary.BuiltBySeverity()
	for _, sev := range []sdk.Severity{sdk.SeverityCritical, sdk.SeverityHigh, sdk.SeverityMedium, sdk.SeverityLow, sdk.SeverityUnknown} {
		n := built[sev]
		parts = append(parts, p.severity(sev, fmt.Sprintf("%d %s", n, sev)))
	}
	fmt.Fprintf(w, "Findings: %s", strings.Join(parts, ", "))
	if n := built[sdk.SeverityNone]; n > 0 {
		fmt.Fprintf(w, ", %d informational", n)
	}
	if res.Summary.Suppressed > 0 {
		fmt.Fprintf(w, " (%d suppressed)", res.Summary.Suppressed)
	}
	fmt.Fprintln(w)
	if n := res.Summary.NotBuilt; n > 0 {
		var nb []string
		for _, sev := range []sdk.Severity{sdk.SeverityCritical, sdk.SeverityHigh, sdk.SeverityMedium, sdk.SeverityLow, sdk.SeverityUnknown, sdk.SeverityNone} {
			if c := res.Summary.NotBuiltBySeverity[sev]; c > 0 {
				nb = append(nb, fmt.Sprintf("%d %s", c, sev))
			}
		}
		fmt.Fprintf(w, "  %s %d in packages that are not built (%s), excluded from the quality gate\n", p.wrap("2", "+"), n, strings.Join(nb, ", "))
	}
	for _, kind := range sortedKeys(toStringMap(res.Summary.ByKind)) {
		fmt.Fprintf(w, "  %s: %d\n", kind, res.Summary.ByKind[sdk.FindingKind(kind)])
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(w, "Warnings: %d (see the report)\n", len(res.Warnings))
	}
	if len(written) > 0 {
		fmt.Fprintf(w, "Reports: %s\n", strings.Join(written, ", "))
	}
	if res.Gate.Threshold != "" {
		status := p.wrap("32", "passed")
		if res.Gate.Failed {
			status = p.wrap("1;31", "FAILED")
		}
		fmt.Fprintf(w, "Quality gate (%s): %s, %d finding(s) at or above threshold\n", res.Gate.Threshold, status, res.Gate.Count)
	}
}

func toStringMap(m map[sdk.FindingKind]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

func newReportCmd(env Env, g *globalOpts) *cobra.Command {
	var from, out string
	var formats []string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Render reports from a saved JSON result without rescanning",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" {
				return usagef("--from is required")
			}
			path := from
			if !filepath.IsAbs(path) {
				path = filepath.Join(env.Cwd, path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return usagef("read result: %w", err)
			}
			var res sdk.ScanResult
			if err := json.Unmarshal(data, &res); err != nil {
				return usagef("%s is not a Deepward JSON result: %w", from, err)
			}
			if res.SchemaVersion != sdk.SchemaVersion {
				return usagef("%s has schemaVersion %d, this Deepward version reads %d", from, res.SchemaVersion, sdk.SchemaVersion)
			}
			s, err := prepare(cmd.Context(), env, g, &runFlags{}, nil)
			if err != nil {
				return err
			}
			if len(formats) == 0 {
				formats = []string{"html"}
			}
			if err := validFormats(formats, s.reporterFormats()); err != nil {
				return err
			}
			dir := firstNonEmpty(out, DefaultOutputDir)
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(env.Cwd, dir)
			}
			written, err := writeReports(cmd.Context(), s.eng, &res, formats, dir, env.Cwd)
			if err != nil {
				return exitCode(ExitRuntime, err)
			}
			if !g.quiet {
				fmt.Fprintf(env.Stdout, "Reports: %s\n", strings.Join(written, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "path of a JSON result written by `deepward scan`")
	cmd.Flags().StringSliceVar(&formats, "format", nil, "report formats (default: html)")
	cmd.Flags().StringVar(&out, "out", "", "output directory (default: deepward-out)")
	return cmd
}
