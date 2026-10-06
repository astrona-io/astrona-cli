package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/manifests"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"gopkg.in/yaml.v3"
)

// kindLabConfig is linked cluster l of cfg as a lab of its own, for
// upLab: l's cluster shape and bootstrap, named <lab>-<name>.
func kindLabConfig(cfg *config.LabConfig, l config.KindLab) *config.LabConfig {
	return &config.LabConfig{
		Metadata:  config.MetadataConfig{Name: cfg.Metadata.Name + "-" + l.Name},
		Runtime:   config.RuntimeConfig{Type: string(runtime.RuntimeKind), Kind: l.Cluster()},
		Bootstrap: l.Bootstrap,
	}
}

// kindLabStates is where cfg's linked clusters are (or will be) for the lab
// running as labCluster, in start order (dependencies first) — the order
// `astrona start` brings them back in.
func kindLabStates(cfg *config.LabConfig, labCluster string) ([]config.KindLab, []cluster.LinkState, error) {
	order, err := config.KindLabOrder(cfg.KindLabs())
	if err != nil {
		return nil, nil, err
	}
	var states []cluster.LinkState
	for _, l := range order {
		states = append(states, cluster.LinkState{Name: l.Name, Cluster: config.KindLabClusterName(labCluster, l.Name)})
	}
	return order, states, nil
}

// startKindLabs creates cfg's linked clusters (runtime.kind.labs) for the
// lab running as labCluster, one at a time in dependency order: each only
// once everything it dependsOn is up and ready (bootstrap and waitFor
// done), with their addresses available to its scripts. The first failure
// stops it — nothing that depends on a broken cluster is started. A
// leftover cluster of the same name (a failed earlier run) is replaced —
// linked clusters belong to the lab, nothing else uses them. The states
// are saved before anything is created, so `astrona destroy` finds every
// cluster even if this fails halfway.
func startKindLabs(cfg *config.LabConfig, baseDir, labCluster string, forTest bool, rep *ui.Reporter) ([]cluster.LinkState, error) {
	order, states, err := kindLabStates(cfg, labCluster)
	if err != nil || len(states) == 0 {
		return nil, err
	}
	if err := cluster.WriteLinks(labCluster, states); err != nil {
		return nil, fmt.Errorf("save linked clusters: %w", err)
	}
	byName := map[string]cluster.LinkState{}
	for i, l := range order {
		name := states[i].Cluster
		var deps []cluster.LinkState
		for _, d := range l.DependsOn {
			deps = append(deps, byName[d])
		}
		sub := kindLabConfig(cfg, l)
		if err := runtime.DestroyEnvironment(name, sub.Runtime, rep); err != nil {
			rep.Warn("could not clean up a previous '%s', proceeding anyway: %s", name, err)
		}
		rep.Section("Linked cluster '%s'", l.Name)
		if _, _, err := upLab(sub, baseDir, name, deps, forTest, rep); err != nil {
			err = fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, name, err)
			if rest := notStarted(order[i+1:]); rest != "" {
				err = fmt.Errorf("%w — not started: %s", err, rest)
			}
			return nil, err
		}
		byName[l.Name] = states[i]
	}
	return states, nil
}

// notStarted names the clusters a failed start skipped, plus the lab.
func notStarted(rest []config.KindLab) string {
	names := make([]string, 0, len(rest)+1)
	for _, l := range rest {
		names = append(names, l.Name)
	}
	return strings.Join(append(names, "the lab itself"), ", ")
}

// attachLinks tells a freshly created cluster where the clusters it uses
// are (the lab: all its linked clusters; a linked cluster: its dependsOn):
// env vars for host scripts and command checks, and a ConfigMap
// astrona-links (namespace default). The lab's own list is saved by
// startKindLabs, for later commands.
func attachLinks(env *runtime.LabEnvironment, clusterName string, links []cluster.LinkState, rep *ui.Reporter) error {
	if len(links) == 0 {
		return nil
	}
	env.WithLinks(links)
	t := rep.Step("Publish links (ConfigMap astrona-links)")
	cm, err := linksConfigMap(links)
	if err != nil {
		return t.Fail(err)
	}
	cmd := exec.Command("kubectl", "--context", env.KubeContext, "apply", "-f", "-")
	cmd.Env = os.Environ()
	if env.Kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+env.Kubeconfig)
	}
	cmd.Stdin = bytes.NewReader(cm)
	cmd.Stdout, cmd.Stderr = t.Output(), t.Output()
	if err := cmd.Run(); err != nil {
		return t.Fail(fmt.Errorf("apply ConfigMap astrona-links: %w", err))
	}
	t.Done()
	return resolveLinkNames(env, links, rep)
}

// resolveLinkNames makes <name>.astrona.internal resolve to each of links
// that exists, inside env's freshly created cluster (waitForClusterDNS
// waits for the CoreDNS restart before bootstrap).
func resolveLinkNames(env *runtime.LabEnvironment, links []cluster.LinkState, rep *ui.Reporter) error {
	return patchLinkNames(env.KubeContext, env.Kubeconfig, linkHosts(links, ""), false, rep)
}

// linkHosts maps the stable name of every link whose cluster exists (other
// than except) to its node's IPv4.
func linkHosts(links []cluster.LinkState, except string) map[string]string {
	hosts := map[string]string{}
	for _, l := range links {
		if l.Cluster == except {
			continue
		}
		if ip, err := cluster.NodeIPv4(l.Cluster); err == nil {
			hosts[l.Hostname()] = ip
		}
	}
	return hosts
}

// refreshLinkNames re-points every stable name of the lab running as
// labCluster at its node's current IPv4, in each of the lab's clusters
// that exists — after all of them are created (an earlier one couldn't
// know a later one's IP), and after a restart or rebuild may have changed
// IPs. skipLab leaves the lab's own cluster alone (already current).
func refreshLinkNames(labCluster string, skipLab bool, rep *ui.Reporter) {
	links, _ := cluster.ReadLinks(labCluster)
	if len(links) == 0 {
		return
	}
	targets := []string{}
	if !skipLab {
		targets = append(targets, labCluster)
	}
	for _, l := range links {
		targets = append(targets, l.Cluster)
	}
	for _, c := range targets {
		if !kindClusterExists(c) {
			continue
		}
		if err := patchLinkNames("kind-"+c, cluster.ExistingKubeconfig(c), linkHosts(links, c), true, rep); err != nil {
			rep.Warn("could not update linked cluster names in %s: %s", c, err)
		}
	}
}

// patchLinkNames writes hosts into a cluster's CoreDNS config and restarts
// CoreDNS (waiting for it when wait is set). A lab-customized Corefile
// without a `.:53` block is left alone — the container names still work.
func patchLinkNames(kubeContext, kubeconfig string, hosts map[string]string, wait bool, rep *ui.Reporter) error {
	t := rep.Step("Resolve linked cluster names in %s (*.%s)", kubeContext, cluster.LinkDomain)
	kubectl := func(args ...string) *exec.Cmd {
		c := exec.Command("kubectl", append([]string{"--context", kubeContext, "-n", "kube-system"}, args...)...)
		c.Env = executor.KubeconfigEnv(kubeconfig)
		return c
	}
	out, err := kubectl("get", "configmap", "coredns", "-o", "jsonpath={.data.Corefile}").Output()
	if err != nil {
		return t.Fail(fmt.Errorf("read CoreDNS config: %w", err))
	}
	corefile, err := cluster.WithLinkHosts(string(out), hosts)
	if err != nil {
		t.Skip("%s — use the container names ($ASTRONA_LINK_<NAME>_HOST)", err)
		return nil
	}
	if corefile == string(out) {
		t.Skip("already up to date")
		return nil
	}
	patch, err := json.Marshal(map[string]any{"data": map[string]string{"Corefile": corefile}})
	if err != nil {
		return t.Fail(err)
	}
	steps := [][]string{
		{"patch", "configmap", "coredns", "--type", "merge", "-p", string(patch)},
		{"rollout", "restart", "deployment/coredns"},
	}
	if wait {
		steps = append(steps, []string{"rollout", "status", "deployment/coredns", "--timeout=2m"})
	}
	for _, args := range steps {
		c := kubectl(args...)
		c.Stdout, c.Stderr = t.Output(), t.Output()
		if err := c.Run(); err != nil {
			return t.Fail(fmt.Errorf("kubectl %s: %w", args[0], err))
		}
	}
	t.Done()
	return nil
}

// linksConfigMap renders the astrona-links ConfigMap: "<link>.host" and
// "<link>.context" per link.
func linksConfigMap(links []cluster.LinkState) ([]byte, error) {
	data := map[string]string{}
	for _, l := range links {
		data[l.Name+".host"] = l.Host()
		data[l.Name+".hostname"] = l.Hostname()
		data[l.Name+".context"] = l.Context()
	}
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "astrona-links", "namespace": "default", "labels": map[string]string{"app.kubernetes.io/managed-by": "astrona"}},
		"data":       data,
	})
}

// labLinks returns a running lab's saved linked clusters, with the
// environment its scripts and checks get for them.
func labLinks(clusterName string) ([]cluster.LinkState, []string) {
	links, err := cluster.ReadLinks(clusterName)
	if err != nil || len(links) == 0 {
		return nil, nil
	}
	return links, cluster.LinkEnv(links)
}

func printLinkHints(w io.Writer, links []cluster.LinkState) {
	if len(links) == 0 {
		return
	}
	fmt.Fprintf(w, "\nLinked clusters (from pods: http://<name>.%s:<nodePort>):\n", cluster.LinkDomain)
	for _, l := range links {
		fmt.Fprintf(w, "    %-10s %s · kubectl --context %s · $%s_HOSTNAME\n", l.Name, l.Hostname(), l.Context(), l.EnvPrefix())
	}
}

// waitForClusterDNS blocks until CoreDNS can answer, so bootstrap scripts
// that resolve names right away — a linked cluster's host, a service — don't
// race it ("bad address" in the first seconds after kind create). Skipped
// when the lab disables kind's CNI without the CNI addon: CoreDNS can't
// start until the lab's own bootstrap installs a CNI.
func waitForClusterDNS(cfg *config.LabConfig, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	if env.Type != runtime.RuntimeKind {
		return nil
	}
	if k := cfg.Runtime.Kind; k != nil && k.Networking.DisableDefaultCNI && k.Addons.CNI == "" {
		return nil
	}
	return manifests.WaitFor([]config.WaitFor{
		{Name: "cluster DNS (CoreDNS)", Resource: "deploy/coredns", Namespace: "kube-system", Timeout: "3m"},
	}, env.KubeContext, rep)
}
