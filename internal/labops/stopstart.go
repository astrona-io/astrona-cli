package labops

import (
	"fmt"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// ResolveKindLab picks the kind lab `stop`/`start` act on: an explicit
// name, else cfg (the lab config from -c/--file/--git, nil when none
// loaded), else the only astrona kind lab whose status want accepts
// (running ones for stop, stopped ones for start). verb is for error
// messages. Neither stop nor start takes the lab's mutation lock.
func ResolveKindLab(labArg string, cfg *config.LabConfig, want func(status string) bool, verb string) (string, error) {
	check := func(name string) (string, error) {
		if hypervisor.StateExists(name) {
			return "", fmt.Errorf("'%s' is a qemu lab — `astrona %s` only supports kind labs for now", name, verb)
		}
		if !cluster.Exists(name) {
			return "", fmt.Errorf("no kind lab named '%s' — run `astrona list` to see what exists", name)
		}
		return name, nil
	}

	if labArg != "" {
		return check(config.NormalizeClusterName(labArg))
	}
	if cfg != nil {
		if cfg.Runtime.Type != "" && cfg.Runtime.Type != string(runtime.RuntimeKind) {
			return "", fmt.Errorf("lab '%s' uses the %s runtime — `astrona %s` only supports kind labs for now", cfg.Metadata.Name, cfg.Runtime.Type, verb)
		}
		return check(config.NormalizeClusterName(cfg.Metadata.Name))
	}

	var matches []string
	owners := LinkedClusterOwners()
	for _, r := range CollectKindRows() {
		if _, linked := owners[r.Name]; !linked && !strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) && want(r.Status) {
			matches = append(matches, r.Name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no astrona kind lab to %s — `astrona list` shows what exists", verb)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("several kind labs match — pick one for `astrona %s`: %s", verb, strings.Join(matches, ", "))
	}
}

// StopPlan is what `astrona stop` will stop: the lab and its linked
// clusters.
type StopPlan struct {
	Lab    string
	Linked []string
}

// Labs is the lab followed by its linked clusters, in stop order.
func (p *StopPlan) Labs() []string { return append([]string{p.Lab}, p.Linked...) }

// PlanStop finds lab's linked clusters and refuses an HA cluster (its node
// IPs change on restart) before anything is touched.
func PlanStop(lab string) (*StopPlan, error) {
	plan := &StopPlan{Lab: lab, Linked: lifecycle.OwnedClusters(lab, nil)}
	for _, l := range plan.Labs() {
		cs, _, err := cluster.KindNodeContainers(l)
		if err != nil {
			return nil, err
		}
		if err := cluster.CheckRestartable(cs); err != nil {
			return nil, fmt.Errorf("%s: %w", l, err)
		}
	}
	return plan, nil
}

// Stop pauses the port forwards and stops the node containers of every
// cluster in plan; progress goes to rep.
func Stop(plan *StopPlan, rep *ui.Reporter) error {
	labs := plan.Labs()
	for _, l := range labs {
		if len(labs) > 1 {
			rep.Section("Lab %s", l)
		}
		if err := stopKindLab(l, rep); err != nil {
			return err
		}
	}
	return nil
}

// stopKindLab pauses lab's port forwards and stops its node containers.
func stopKindLab(lab string, rep *ui.Reporter) error {
	if portforward.Count(lab) > 0 {
		t := rep.Step("Pause port forwards")
		if _, err := portforward.Pause(lab); err != nil {
			return t.Fail(err)
		}
		t.Done()
	}
	return cluster.StopKindCluster(lab, rep)
}

// StartResult is what `astrona start` did, for cmd to print.
type StartResult struct {
	Lab      string
	Linked   []string              // linked clusters started before the lab
	Health   string                // the lab's API health after the start
	Forwards []portforward.Forward // the port forwards restarted
}

// Start resumes a stopped kind lab: its linked clusters first (the lab's
// workloads may call them, and their WAN conditions are re-applied), then
// the lab itself, its port forwards and its link names. Progress goes to
// rep.
func Start(lab string, rep *ui.Reporter) (*StartResult, error) {
	linked := lifecycle.OwnedClusters(lab, nil)
	saved, _ := cluster.ReadLinks(lab)
	for _, c := range linked {
		rep.Section("Linked cluster %s", c)
		if _, err := startKindLab(c, rep); err != nil {
			return nil, fmt.Errorf("linked cluster %s: %w", c, err)
		}
		// A restart drops netem — re-apply the configured conditions.
		for _, l := range saved {
			if l.Cluster == c {
				if err := lifecycle.ApplyWAN(c, l.WAN, rep); err != nil {
					rep.Warn("could not re-apply wan conditions on %s: %s", c, err)
				}
			}
		}
	}
	if len(linked) > 0 {
		rep.Section("Lab %s", lab)
	}
	forwards, err := startKindLab(lab, rep)
	if err != nil {
		return nil, err
	}
	// Node IPs may have changed with the restart.
	lifecycle.RefreshLinkNames(lab, false, rep)

	health, _ := KindAPIHealth(lab)
	return &StartResult{Lab: lab, Linked: linked, Health: health, Forwards: forwards}, nil
}

// startKindLab starts lab's node containers, waits for its API, and
// restarts its saved port forwards.
func startKindLab(lab string, rep *ui.Reporter) ([]portforward.Forward, error) {
	if err := cluster.StartKindCluster(lab, rep); err != nil {
		return nil, err
	}
	if err := cluster.WaitForDefaultServiceAccount("kind-"+lab, cluster.ExistingKubeconfig(lab), 3*time.Minute, rep); err != nil {
		return nil, err
	}
	saved := savedForwards(lab)
	if len(saved) == 0 {
		return nil, nil
	}
	rep.Section("Port forwards")
	forwards, err := lifecycle.StartPortForwards(lab, saved, rep)
	if err != nil {
		rep.Warn("some port forwards could not be restarted — `astrona port-forward start -c <config>`")
	}
	return forwards, nil
}

// savedForwards returns the port forward specs recorded for lab (kept by
// portforward.Pause while it's stopped).
func savedForwards(lab string) []config.PortForward {
	fs, err := portforward.List(lab)
	if err != nil {
		return nil
	}
	out := make([]config.PortForward, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Spec.Forward)
	}
	return out
}
