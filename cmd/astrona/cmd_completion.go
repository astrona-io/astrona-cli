package main

import (
	"strings"

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
