package main

import (
	"fmt"
	"slices"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"

	"github.com/spf13/cobra"
)

// uiSubcommands are the names `astrona ui <lab>` cannot take as a short lab
// name: a lab called status, stop or serve is opened by its full name.
var uiSubcommands = []string{"serve", "status", "stop"}

func newWorkspaceCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui [lab-name]",
		Short: "Open a running lab, or every lab on this computer, in the browser",
		Long: "Open a running Kind lab in the browser workspace: astrona starts the local workspace server " +
			"(or reuses a running one), prints a link with a fresh single-use code (valid for two minutes) and " +
			"opens it in your browser. With no browser on this machine (no display), open the printed link yourself. " +
			"Use it when a link has expired or the browser tab was closed; it never restarts the lab or its timer, " +
			"and it uses the saved local files without checking GitHub.\n\n" +
			"Without a lab name it opens the only running lab. With none or several it opens \"Labs on this computer\", " +
			"a page listing every lab and playground that still runs here (labs keep running after astrona run returns), " +
			"where you open or end them. " +
			"A lab named status, stop or serve is opened by its full name (" + apphome.ClusterPrefix() + "status), " +
			"since the short name is a subcommand.\n\n" +
			"QEMU labs do not open in the browser yet: astrona status <lab> shows the astrona ssh command for each VM.\n\n" +
			"astrona ui status shows the workspace server and a fresh link; astrona ui stop stops it (labs keep running).",
		Example: "  astrona ui\n  astrona ui my-lab\n  astrona ui " + apphome.ClusterPrefix() + "status   # a lab named status\n  astrona ui status\n  astrona ui stop",
		Args:    cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names := labops.RunningLabNames()
			rows := make([]labops.LabRow, 0, len(names))
			for _, name := range names {
				rows = append(rows, labops.LabRow{Name: name, Status: "Running"})
			}
			return labNameCandidates(rows, nil, prefix), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			running := labops.RunningLabNames()
			slices.Sort(running)
			var name string
			if len(args) == 0 {
				out := cmd.OutOrStdout()
				switch len(running) {
				case 0:
					fmt.Fprintln(out, "No lab is running. Start one with astrona run <lab>.")
					return openWorkspaceHome(out)
				case 1:
					name = running[0]
				default:
					fmt.Fprintln(out, "Several labs are running (astrona ui <lab> opens one):")
					for _, n := range running {
						fmt.Fprintf(out, "  %s\n", uiLabArg(n))
					}
					return openWorkspaceHome(out)
				}
			} else {
				name = config.NormalizeClusterName(args[0])
				if !slices.Contains(running, name) {
					return fmt.Errorf("no running lab named %s; astrona list shows running labs, astrona run <lab> starts one", name)
				}
			}
			st, err := labstate.Load(name)
			if err != nil {
				return err
			}
			cfg, baseDir, cleanup, err := loadSavedLab(name, st, flags)
			if err != nil {
				return err
			}
			defer cleanup()
			if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
				return fmt.Errorf("lab %s runs on %s, which the browser workspace does not open yet; astrona status %s shows the astrona ssh command for each VM", name, cfg.Runtime.Type, uiLabArg(name))
			}
			return reopenBrowserWorkspace(cfg, baseDir, flags)
		},
	}
	// The browser workspace server. A lab named serve, status or stop is
	// opened by its full name (uiLabArg).
	cmd.AddCommand(newUIServeCmd(), newUIStatusCmd(), newUIStopCmd())
	return cmd
}

// uiLabArg is how to name a lab to astrona ui: the short name, or the full
// cluster name when the short one is a subcommand of ui.
func uiLabArg(clusterName string) string {
	short := strings.TrimPrefix(clusterName, apphome.ClusterPrefix())
	if slices.Contains(uiSubcommands, short) {
		return clusterName
	}
	return short
}

// loadSavedLab loads the config a running lab was started from, from the
// saved local files (no fetch, no update), and points flags at it.
func loadSavedLab(name string, st *labstate.State, flags *rootFlags) (*config.LabConfig, string, func(), error) {
	noop := func() {}
	if st == nil || st.Source == nil {
		return nil, "", noop, fmt.Errorf("run %s has no saved config source", name)
	}
	applySource(flags, st.Source)
	lab, err := labops.LoadSavedLab(name, labOpsSource(flags), labops.LoadOptions{Version: Version})
	if err != nil {
		return nil, "", noop, err
	}
	return lab.Config, lab.BaseDir, lab.Cleanup, nil
}
