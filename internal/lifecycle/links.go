package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/manifests"
	"astrona/internal/runtime"
	"astrona/internal/ui"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// AttachLinks tells a freshly created cluster where the clusters it uses
// are (the lab: all its linked clusters; a linked cluster: its dependsOn):
// env vars for host scripts and command checks, and a ConfigMap
// astrona-clusters (namespace default). The lab's own list is saved by
// StartLinkedClusters, for later commands.
func AttachLinks(env *runtime.LabEnvironment, clusterName string, links []cluster.LinkState, rep *ui.Reporter) error {
	if len(links) == 0 {
		return nil
	}
	env.WithLinks(links)
	t := rep.Step("Publish linked clusters (ConfigMap astrona-clusters)")
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
		return t.Fail(fmt.Errorf("apply ConfigMap astrona-clusters: %w", err))
	}
	t.Done()
	return resolveLinkNames(env, links, rep)
}

// resolveLinkNames makes <name>.astrona.internal resolve to each of links
// that exists, inside env's freshly created cluster (WaitForClusterDNS
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

// RefreshLinkNames re-points every stable name of the lab running as
// labCluster at its node's current IPv4, in each of the lab's clusters
// that exists — after all of them are created (an earlier one couldn't
// know a later one's IP), and after a restart or rebuild may have changed
// IPs. skipLab leaves the lab's own cluster alone (already current).
func RefreshLinkNames(labCluster string, skipLab bool, rep *ui.Reporter) {
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
		if !cluster.Exists(c) {
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
		t.Skip("%s — use the container names ($ASTRONA_CLUSTER_<NAME>_HOST)", err)
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

// linksConfigMap renders the astrona-clusters ConfigMap: "<name>.host" and
// "<link>.context" per link.
func linksConfigMap(links []cluster.LinkState) ([]byte, error) {
	data := map[string]string{}
	for _, l := range links {
		data[l.Name+".host"] = l.Host()
		data[l.Name+".hostname"] = l.Hostname()
		data[l.Name+".context"] = l.Context()
	}
	// astrona-clusters, plus the same data under its pre-v0.3 name
	// astrona-links so labs written for that keep working.
	var out []byte
	for i, name := range []string{"astrona-clusters", "astrona-links"} {
		doc, err := yaml.Marshal(map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": name, "namespace": "default", "labels": map[string]string{"app.kubernetes.io/managed-by": "astrona"}},
			"data":       data,
		})
		if err != nil {
			return nil, err
		}
		if i > 0 {
			out = append(out, []byte("---\n")...)
		}
		out = append(out, doc...)
	}
	return out, nil
}

// Links returns a running lab's saved linked clusters, with the
// environment its scripts and checks get for them.
func Links(clusterName string) ([]cluster.LinkState, []string) {
	links, err := cluster.ReadLinks(clusterName)
	if err != nil || len(links) == 0 {
		return nil, nil
	}
	return links, cluster.LinkEnv(links)
}

// WaitForClusterDNS blocks until CoreDNS can answer, so bootstrap scripts
// that resolve names right away — a linked cluster's host, a service — don't
// race it ("bad address" in the first seconds after kind create). Skipped
// when the lab disables kind's CNI without the CNI addon: CoreDNS can't
// start until the lab's own bootstrap installs a CNI.
func WaitForClusterDNS(cfg *config.LabConfig, env *runtime.LabEnvironment, rep *ui.Reporter) error {
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

// FindLinkedCluster finds the kind cluster of linked cluster name in lab's
// links, with a helpful error naming the ones it has.
func FindLinkedCluster(lab, name string, links []cluster.LinkState) (string, error) {
	var names []string
	for _, l := range links {
		if l.Name == name {
			return l.Cluster, nil
		}
		names = append(names, l.Name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("lab %s has no linked clusters (runtime.kind.clusters)", lab)
	}
	return "", fmt.Errorf("lab %s has no linked cluster '%s' — it has: %s", lab, name, strings.Join(names, ", "))
}
