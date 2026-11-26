package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/internal/toolchain"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// DefaultTools builds the real toolchain from the registered resolver plugins.
func DefaultTools(_ context.Context, cfg engine.Config, ws *sdk.Workspace, reg *sdk.Registry, cacheDir string, env Env) (sdk.Tools, error) {
	var resolvers []sdk.ToolResolver
	var defs []sdk.ToolDefinition
	for _, p := range reg.All() {
		if disabled(cfg, p.Info().ID) {
			continue
		}
		if r, ok := p.(sdk.ToolResolver); ok && p.Info().Kind == sdk.KindToolResolver {
			resolvers = append(resolvers, r)
		}
		if d, ok := p.(sdk.ToolDefiner); ok {
			defs = append(defs, d.ToolDefinitions()...)
		}
	}
	environ := os.Environ()
	if env.Environ != nil {
		environ = env.Environ()
	}
	var secrets []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "DEEPWARD_") && (strings.Contains(k, "TOKEN") || strings.Contains(k, "PASSWORD")) && v != "" {
			secrets = append(secrets, v)
		}
	}
	tc, err := toolchain.New(toolchain.Options{Resolvers: resolvers, Definitions: defs, Settings: cfg.Tools, Workspace: ws,
		CacheDir: cacheDir, Environ: environ, Getenv: env.getenv, Secrets: secrets})
	if err != nil {
		return nil, usage(err)
	}
	return tc, nil
}

func disabled(cfg engine.Config, id string) bool {
	node, ok := cfg.Plugins[id]
	if !ok {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "enabled" && node.Content[i+1].Value == "false" {
			return true
		}
	}
	return false
}

type requirement struct {
	req     sdk.ToolRequirement
	project *sdk.Project
	by      string
}

// requirements collects the tools needed by enabled plugins and setup steps.
func (s *session) requirements(ctx context.Context) []requirement {
	var out []requirement
	projects := s.eng.Detect(ctx, s.eng.Workspace(), nil)
	for _, p := range s.eng.Enabled(sdk.KindEcosystem) {
		tr, ok := p.(sdk.ToolRequirer)
		if !ok {
			continue
		}
		for i := range projects {
			if projects[i].Ecosystem != p.Info().ID {
				continue
			}
			for _, r := range tr.ToolRequirements() {
				out = append(out, requirement{req: r, project: &projects[i], by: p.Info().ID})
			}
		}
	}
	for _, kind := range []sdk.Kind{sdk.KindAnalyzer, sdk.KindFactProducer, sdk.KindReporter, sdk.KindPlatform} {
		for _, p := range s.eng.Enabled(kind) {
			if tr, ok := p.(sdk.ToolRequirer); ok {
				for _, r := range tr.ToolRequirements() {
					out = append(out, requirement{req: r, by: p.Info().ID})
				}
			}
		}
	}
	for i := range projects {
		steps := append([]engine.SetupStep{}, s.cfg.Ecosystems[projects[i].Ecosystem].Setup...)
		steps = append(steps, s.cfg.Projects[projects[i].Dir].Setup...)
		for _, st := range steps {
			out = append(out, requirement{req: sdk.ToolRequirement{Name: st.Tool, Purpose: "setup step"}, project: &projects[i], by: "setup"})
		}
	}
	return out
}

func (s *session) doctor(ctx context.Context) error {
	tc, ok := s.tools.(*toolchain.Toolchain)
	if !ok {
		return exitCode(ExitRuntime, fmt.Errorf("no toolchain configured"))
	}
	reqs := s.requirements(ctx)
	tw := tabwriter.NewWriter(s.env.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tPROJECT\tREQUIRED BY\tRESOLVER\tVERSION\tSTATUS\tSOURCE")
	failed := 0
	var details []string
	for _, r := range reqs {
		d := tc.Diagnose(ctx, r.req, r.project)
		project := "-"
		if r.project != nil {
			project = r.project.ID
		}
		status := "ok"
		if !d.OK {
			failed++
			status = "not found"
			if d.Err != nil {
				status = strings.SplitN(d.Err.Error(), ":", 2)[0]
			}
			var tried []string
			for _, a := range d.Attempts {
				tried = append(tried, a.Resolver+": "+a.Outcome)
			}
			details = append(details, fmt.Sprintf("%s for %s: %s", r.req.Name, project, strings.Join(tried, "; ")))
		}
		name := r.req.Name
		if r.req.Constraint != "" {
			name += " " + r.req.Constraint
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, project, r.by, firstNonEmpty(d.Resolver, "-"),
			firstNonEmpty(d.Version, "-"), status, firstNonEmpty(relTo(s.root, d.Source), "-"))
	}
	_ = tw.Flush()
	if len(reqs) == 0 {
		fmt.Fprintln(s.env.Stdout, "no enabled plugin requires external tools for this repository")
	}
	for _, d := range details {
		fmt.Fprintln(s.env.Stdout, "  "+d)
	}
	if failed > 0 {
		return exitCode(ExitRuntime, fmt.Errorf("%d tool requirement(s) not satisfied", failed))
	}
	return nil
}

func relTo(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(p) {
		return filepath.ToSlash(rel)
	}
	return p
}
