package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"astrona/internal/cluster"
	"astrona/internal/config"
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
// running as labCluster.
func kindLabStates(cfg *config.LabConfig, labCluster string) []cluster.LinkState {
	var states []cluster.LinkState
	for _, l := range cfg.KindLabs() {
		states = append(states, cluster.LinkState{Name: l.Name, Cluster: config.KindLabClusterName(labCluster, l.Name)})
	}
	return states
}

// startKindLabs creates cfg's linked clusters (runtime.kind.labs) for the
// lab running as labCluster, each with its own preload, addons, bootstrap
// and readiness gates, and returns where they are. A leftover cluster of
// the same name (a failed earlier run) is replaced — linked clusters
// belong to the lab, nothing else uses them. The states are saved before
// anything is created, so `astrona destroy` finds every cluster even if
// this fails halfway.
func startKindLabs(cfg *config.LabConfig, baseDir, labCluster string, forTest bool, rep *ui.Reporter) ([]cluster.LinkState, error) {
	states := kindLabStates(cfg, labCluster)
	if len(states) == 0 {
		return nil, nil
	}
	if err := cluster.WriteLinks(labCluster, states); err != nil {
		return nil, fmt.Errorf("save linked clusters: %w", err)
	}
	for i, l := range cfg.KindLabs() {
		name := states[i].Cluster
		sub := kindLabConfig(cfg, l)
		if err := runtime.DestroyEnvironment(name, sub.Runtime, rep); err != nil {
			rep.Warn("could not clean up a previous '%s', proceeding anyway: %s", name, err)
		}
		rep.Section("Linked cluster '%s'", l.Name)
		if _, _, err := upLab(sub, baseDir, name, nil, forTest, rep); err != nil {
			return nil, fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, name, err)
		}
	}
	return states, nil
}

// attachLinks tells a freshly created lab where its linked clusters are:
// env vars for host scripts and command checks, saved state for later
// commands, and a ConfigMap astrona-links (namespace default) in the
// cluster.
func attachLinks(env *runtime.LabEnvironment, clusterName string, links []cluster.LinkState, rep *ui.Reporter) error {
	if len(links) == 0 {
		return nil
	}
	env.WithLinks(links)
	if err := cluster.WriteLinks(clusterName, links); err != nil {
		return fmt.Errorf("save linked clusters: %w", err)
	}
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
	return nil
}

// linksConfigMap renders the astrona-links ConfigMap: "<link>.host" and
// "<link>.context" per link.
func linksConfigMap(links []cluster.LinkState) ([]byte, error) {
	data := map[string]string{}
	for _, l := range links {
		data[l.Name+".host"] = l.Host()
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
	fmt.Fprintf(w, "\nLinked clusters (reach a NodePort service at <host>:<nodePort> from pods and scripts):\n")
	for _, l := range links {
		fmt.Fprintf(w, "    %-10s host %s · kubectl --context %s · $%s_HOST\n", l.Name, l.Host(), l.Context(), l.EnvPrefix())
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
