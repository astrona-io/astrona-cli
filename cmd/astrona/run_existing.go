package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"astrona/internal/config"
)

// runChoice is what `astrona run` does about labs that are already running.
type runChoice struct {
	startOver  bool     // the lab itself is running: destroy it, then build it fresh
	keep       bool     // the lab itself is running and stays as it is: nothing to build
	stopOthers []string // other running labs to destroy first (cluster names)
}

// chooseRun decides, before anything is built, what to do about running
// labs: the lab being started (exists) and the others. Interactive (tty)
// it asks; --yes starts the lab over without asking but never touches the
// other labs; without a terminal and without --yes a running lab is an
// error naming --yes, never a prompt that blocks.
func chooseRun(in io.Reader, out io.Writer, tty, yes, exists bool, label string, others []string) (runChoice, error) {
	var c runChoice
	// One buffered reader for every question: confirmYes reuses it rather
	// than buffering ahead and swallowing the next answer.
	in = bufio.NewReader(in)
	if exists {
		switch {
		case yes:
			c.startOver = true
		case !tty:
			return c, fmt.Errorf("lab %s is already running — pass --yes to destroy it and start over (or `astrona reset`)", label)
		case confirmYes(in, out, fmt.Sprintf("Lab %s is already running. Destroy it and start over? Everything done in it is lost.", label)):
			c.startOver = true
		default:
			c.keep = true
			return c, nil
		}
	}
	if tty && !yes && len(others) > 0 {
		names := make([]string, len(others))
		for i, o := range others {
			names[i] = strings.TrimPrefix(o, "astro-")
		}
		if confirmYes(in, out, fmt.Sprintf("Also running: %s. Destroy %s first? Everything done in %s is lost.",
			strings.Join(names, ", "), pick(len(others), "it", "them"), pick(len(others), "it", "them"))) {
			c.stopOthers = others
		}
	}
	return c, nil
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// otherRunningLabNames are the running labs — kind clusters and qemu VMs —
// other than clusterName and its own VMs and linked clusters, as cluster
// names. Test copies and stopped clusters are left out.
func otherRunningLabNames(clusterName string) []string {
	var out []string
	for _, name := range otherRunningLabs(clusterName) { // kind; already without linked clusters
		out = append(out, config.NormalizeClusterName(name))
	}
	rows, _, err := collectQEMURows()
	if err != nil {
		return out
	}
	return append(out, otherQEMULabs(clusterName, rows)...)
}

// otherQEMULabs are the labs of the running qemu VMs in rows, other than
// clusterName — one name per lab, so a multi-VM lab ("astro-net" with VMs
// astro-net-vm1, astro-net-vm2) is destroyed and forgotten as the lab it
// is. Test copies are left out.
func otherQEMULabs(clusterName string, rows []labRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		lab := r.lab
		if lab == "" {
			// A handle from before VMs recorded their lab: a VM named
			// "<clusterName>-…" may be one of this lab's own, so leave it be
			// (it can't be told from an unrelated lab with a longer name).
			if strings.HasPrefix(r.name, clusterName+"-") {
				continue
			}
			lab = r.name
		}
		if lab == clusterName || strings.HasPrefix(lab, "astro-test-") || seen[lab] {
			continue
		}
		seen[lab] = true
		out = append(out, lab)
	}
	return out
}
