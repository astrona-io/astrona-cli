package main

import (
	"strings"

	"astrona/internal/config"

	"github.com/spf13/cobra"
)

// labCompletion returns a ValidArgsFunction completing the lab names
// `astrona list` shows (qemu VMs and kind clusters) that keep accepts —
// nil keep means every lab. Only the first positional argument is
// completed. The cheap discovery (no API health queries) keeps <TAB>
// instant.
//
// Names are offered as listed ("astro-my-lab"); once the user has typed
// something that isn't a prefix of "astro-", the unprefixed form
// ("my-lab") is offered instead — every lab-name argument accepts both.
func labCompletion(keep func(labRow) bool) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		qemu, _, _ := collectQEMURows()
		return labNameCandidates(append(qemu, collectKindRows()...), keep, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func labNameCandidates(rows []labRow, keep func(labRow) bool, toComplete string) []cobra.Completion {
	unprefixed := toComplete != "" && !strings.HasPrefix("astro-", toComplete) && !strings.HasPrefix(toComplete, "astro-")
	seen := map[string]bool{}
	var out []cobra.Completion
	for _, r := range rows {
		if keep != nil && !keep(r) {
			continue
		}
		name := r.name
		if unprefixed {
			name = strings.TrimPrefix(name, "astro-")
		}
		if seen[name] || !strings.HasPrefix(name, toComplete) {
			continue
		}
		seen[name] = true
		out = append(out, cobra.CompletionWithDesc(name, r.runtime+", "+r.status))
	}
	return out
}

func isKind(r labRow) bool    { return r.runtime == "kind" }
func isQEMU(r labRow) bool    { return r.runtime == "qemu" }
func isRunning(r labRow) bool { return r.status != "Stopped" }
func isStoppedKind(r labRow) bool {
	return isKind(r) && (r.status == "Stopped" || strings.HasPrefix(r.status, "Degraded"))
}

// clusterFlagCompletion completes --cluster with the lab's linked
// clusters: from a lab-name argument's saved state when there is one
// (shell, kubeconfig, net), else from the lab config the command would use
// (-c, the current directory, `astrona use`). Reads local files only — it
// never clones a --git lab or hands over to another astrona version.
func clusterFlagCompletion(flags *rootFlags, nameArg bool) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var names []string
		if nameArg && len(args) > 0 {
			links, _ := labLinks(config.NormalizeClusterName(args[0]))
			for _, l := range links {
				names = append(names, l.Name)
			}
		} else {
			applyCurrentLab(cmd, flags)
			if !nameArg {
				useLabArg(args, flags)
			}
			if flags.gitURL == "" {
				if path, err := config.ResolveConfigPath(flags.configPath, flags.fileName, "", "", false); err == nil {
					if cfg, cleanup, err := config.LoadLabConfig(path); err == nil {
						cleanup()
						for _, l := range cfg.KindClusters() {
							names = append(names, l.Name)
						}
					}
				}
			}
		}
		var out []cobra.Completion
		for _, n := range names {
			if strings.HasPrefix(n, toComplete) {
				out = append(out, cobra.CompletionWithDesc(n, "linked cluster"))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// versionCompletion completes `versions install` with published releases
// not installed yet (a quick lookup — nothing offered when offline), or
// `versions remove` with installed ones.
func versionCompletion(installedOnly bool) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		installed := map[string]bool{}
		var out []cobra.Completion
		for _, iv := range installedVersions() {
			installed[iv.v.String()] = true
			if installedOnly && strings.HasPrefix(iv.v.String(), toComplete) {
				out = append(out, cobra.CompletionWithDesc(iv.v.String(), "installed"))
			}
		}
		if !installedOnly {
			tags, _ := quickReleaseTags()
			for _, t := range tags {
				v := strings.TrimPrefix(t, "v")
				if !installed[v] && strings.HasPrefix(v, toComplete) {
					out = append(out, cobra.CompletionWithDesc(v, "release"))
				}
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	}
}
