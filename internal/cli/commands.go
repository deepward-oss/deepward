package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// versionLine is printed by `deepward version` and `deepward --version`.
func versionLine(env Env) string {
	return fmt.Sprintf("deepward %s (commit %s, built %s, sdk %s)",
		firstNonEmpty(env.Version, "dev"), firstNonEmpty(env.Commit, "unknown"), firstNonEmpty(env.Date, "unknown"), sdk.SDKVersion)
}

func newVersionCmd(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintln(env.Stdout, versionLine(env))
			return nil
		},
	}
}

func newPluginsCmd(env Env, g *globalOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "plugins", Short: "Inspect plugins"}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List registered plugins with their kind, version and enabled state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := prepare(cmd.Context(), env, g, &runFlags{}, nil)
			if err != nil {
				return err
			}
			plugins := s.eng.Plugins()
			for i := range plugins {
				if plugins[i].Info.Version == sdk.BuiltinVersion {
					plugins[i].Info.Version = firstNonEmpty(env.Version, "dev")
				}
			}
			if asJSON {
				enc := json.NewEncoder(env.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"sdkVersion": sdk.SDKVersion, "plugins": plugins})
			}
			fmt.Fprintf(env.Stdout, "SDK %s\n", sdk.SDKVersion)
			tw := tabwriter.NewWriter(env.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "KIND\tID\tVERSION\tENABLED\tDESCRIPTION")
			for _, p := range plugins {
				enabled := "yes"
				if !p.Enabled {
					enabled = "no"
				}
				desc := p.Info.Description
				if p.Info.Integration {
					desc = "[integration] " + desc
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Info.Kind, p.Info.ID, p.Info.Version, enabled, desc)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(list)
	return cmd
}

func newConfigCmd(env Env, g *globalOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the configuration"}
	validate := &cobra.Command{
		Use:   "validate [root]",
		Short: "Validate deepward.yaml and print the effective configuration with secrets masked",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := prepare(cmd.Context(), env, g, &runFlags{}, args)
			if err != nil {
				return err
			}
			out, err := maskedConfigYAML(s.cfg)
			if err != nil {
				return exitCode(ExitRuntime, err)
			}
			file := s.file
			if file == "" {
				file = "(no configuration file, defaults)"
			}
			fmt.Fprintf(env.Stdout, "configuration is valid: %s\n", file)
			if strings.TrimSpace(out) != "{}" {
				fmt.Fprint(env.Stdout, out)
			}
			return nil
		},
	}
	cmd.AddCommand(validate)
	return cmd
}

func newDBCmd(env Env, g *globalOpts) *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Manage local vulnerability data"}
	r := &runFlags{}
	update := &cobra.Command{
		Use:   "update",
		Short: "Download or refresh the offline vulnerability database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := prepare(cmd.Context(), env, g, r, nil)
			if err != nil {
				return err
			}
			updated := 0
			for _, p := range s.eng.Plugins() {
				if !p.Enabled {
					continue
				}
				u, ok := s.eng.Plugin(p.Info.ID).(sdk.DataUpdater)
				if !ok {
					continue
				}
				summary, err := u.UpdateData(cmd.Context())
				if err != nil {
					return exitCode(ExitRuntime, fmt.Errorf("%s: %w", p.Info.ID, err))
				}
				updated++
				if !g.quiet {
					fmt.Fprintf(env.Stdout, "%s: %s\n", p.Info.ID, summary)
				}
			}
			if updated == 0 {
				return exitCode(ExitUsage, fmt.Errorf("no enabled plugin keeps local data"))
			}
			return nil
		},
	}
	r.register(update, false)
	cmd.AddCommand(update)
	return cmd
}

func newDoctorCmd(env Env, g *globalOpts) *cobra.Command {
	r := &runFlags{}
	cmd := &cobra.Command{
		Use:   "doctor [root]",
		Short: "Resolve every tool required by enabled plugins and print where it was found",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := prepare(cmd.Context(), env, g, r, args)
			if err != nil {
				return err
			}
			return s.doctor(cmd.Context())
		},
	}
	r.register(cmd, false)
	return cmd
}

func newFixCmd(env Env, g *globalOpts) *cobra.Command {
	r := &runFlags{}
	f := &fixFlags{}
	cmd := &cobra.Command{
		Use:   "fix [root]",
		Short: "Prepare dependency updates and fixes; with --publish open merge/pull requests",
		Long: "Without --publish, fix prints a unified diff of the proposed edits and writes nothing to the remote.\n" +
			"With --publish and a DEEPWARD_GITLAB_TOKEN or DEEPWARD_GITHUB_TOKEN, it creates or updates change requests.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.check(env); err != nil {
				return err
			}
			s, err := prepare(cmd.Context(), env, g, r, args)
			if err != nil {
				return err
			}
			return s.fix(cmd.Context(), f)
		},
	}
	r.register(cmd, false)
	f.register(cmd)
	return cmd
}
