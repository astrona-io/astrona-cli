package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/manifests"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"gopkg.in/yaml.v3"
)

// maxLinkDepth bounds link chains (a lab linking a lab linking a lab …).
const maxLinkDepth = 3

// linkedLab is a resolved link: which cluster it is and, when the link is
// a path, the linked lab's config to start it from.
type linkedLab struct {
	link    config.Link
	cluster string
	cfg     *config.LabConfig // nil for a link by running-lab name
	baseDir string
	flags   *rootFlags // flags to load/trust the linked lab with
}

// resolveLink finds what l points at. A path is resolved against baseDir;
// for a --git lab it must stay inside the cloned repo, and a URL-only
// config can't link by path at all — a remote lab must never start a
// config from elsewhere on the student's disk.
func resolveLink(l config.Link, baseDir string, flags *rootFlags) (*linkedLab, error) {
	if !l.IsPath() {
		return &linkedLab{link: l, cluster: config.NormalizeClusterName(l.Lab)}, nil
	}
	if strings.HasPrefix(baseDir, "http://") || strings.HasPrefix(baseDir, "https://") || strings.HasPrefix(baseDir, "https:") {
		return nil, fmt.Errorf("link '%s': a lab loaded from a URL can only link to a running lab by name, not to a path", l.Name)
	}
	target := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(l.Lab)))

	linkFlags := *flags
	// The linked lab has its own config.yaml; -f/--file names this lab's.
	linkFlags.fileName = "config.yaml"
	if flags.gitURL != "" {
		out, err := exec.Command("git", "-C", baseDir, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return nil, fmt.Errorf("link '%s': can't find the lab's git checkout: %w", l.Name, err)
		}
		root, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
		resolved, _ := filepath.EvalSymlinks(target)
		if resolved == "" {
			resolved = target
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("link '%s': '%s' is outside the lab's git repository", l.Name, l.Lab)
		}
		// Load (and trust-check) the linked lab as the same repo, at
		// that subdirectory.
		linkFlags.configPath = rel
	} else {
		linkFlags.configPath = target
	}

	cfg, linkBase, cleanup, err := LoadLabForCommand(&linkFlags)
	if err != nil {
		return nil, fmt.Errorf("link '%s': %w", l.Name, err)
	}
	cleanup()
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return nil, fmt.Errorf("link '%s': %s is a %s lab — only kind labs can be linked", l.Name, cfg.Metadata.Name, cfg.Runtime.Type)
	}
	return &linkedLab{link: l, cluster: config.NormalizeClusterName(cfg.Metadata.Name), cfg: cfg, baseDir: linkBase, flags: &linkFlags}, nil
}

// startLinkedLabs makes sure every lab cfg links to is running — reusing a
// running one, starting one from its config otherwise (after the same
// trust check as any lab) — and returns where they are. visited holds the
// clusters already on the current chain, so a link cycle is an error.
func startLinkedLabs(cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter, visited map[string]bool, depth int) ([]cluster.LinkState, error) {
	if len(cfg.Links) == 0 {
		return nil, nil
	}
	if depth >= maxLinkDepth {
		return nil, fmt.Errorf("links nest deeper than %d levels", maxLinkDepth)
	}
	var states []cluster.LinkState
	for _, l := range cfg.Links {
		ll, err := resolveLink(l, baseDir, flags)
		if err != nil {
			return nil, err
		}
		if visited[ll.cluster] {
			return nil, fmt.Errorf("link '%s' leads back to %s — links can't form a cycle", l.Name, ll.cluster)
		}
		states = append(states, cluster.LinkState{Name: l.Name, Cluster: ll.cluster})

		if kindClusterExists(ll.cluster) {
			rep.Info("Link '%s': using running lab %s.", l.Name, ll.cluster)
			continue
		}
		if ll.cfg == nil {
			return nil, fmt.Errorf("link '%s': lab %s isn't running — start it first, or link to its config path", l.Name, ll.cluster)
		}
		if err := validateLabForRun(ll.cfg); err != nil {
			return nil, fmt.Errorf("link '%s' (%s): %w", l.Name, ll.cfg.Metadata.Name, err)
		}
		if err := requireTrust(ll.flags, ll.cfg, ll.baseDir); err != nil {
			return nil, fmt.Errorf("link '%s': %w", l.Name, err)
		}

		next := map[string]bool{ll.cluster: true}
		for k := range visited {
			next[k] = true
		}
		sub, err := startLinkedLabs(ll.cfg, ll.baseDir, ll.flags, rep, next, depth+1)
		if err != nil {
			return nil, err
		}
		rep.Section("Linked lab '%s'", l.Name)
		if _, _, err := upLab(ll.cfg, ll.baseDir, ll.cluster, sub, false, rep); err != nil {
			return nil, fmt.Errorf("linked lab '%s' (%s): %w", l.Name, ll.cluster, err)
		}
	}
	return states, nil
}

// attachLinks tells a freshly created lab where its links are: env vars
// for host scripts and command checks, saved link state for later
// commands, and a ConfigMap astrona-links (namespace default) for the
// cluster.
func attachLinks(env *runtime.LabEnvironment, clusterName string, links []cluster.LinkState, rep *ui.Reporter) error {
	if len(links) == 0 {
		return nil
	}
	env.WithEnv(cluster.LinkEnv(links))
	if err := cluster.WriteLinks(clusterName, links); err != nil {
		return fmt.Errorf("save links: %w", err)
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

// labLinks returns a running lab's saved links, with the environment its
// scripts and checks get for them.
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
	fmt.Fprintf(w, "\nLinked labs (reach a NodePort service at <host>:<nodePort> from pods and scripts):\n")
	for _, l := range links {
		fmt.Fprintf(w, "    %-10s host %s · kubectl --context %s · $%s_HOST\n", l.Name, l.Host(), l.Context(), l.EnvPrefix())
	}
}

// startTestLinks brings cfg's linked labs up as throwaway test clusters
// (astro-test-<lab>, no port forwards or gateway host ports) for `astrona
// test`, and returns their states plus a teardown that destroys every
// test cluster it created — call it even on error. A link by running-lab
// name uses that lab as-is and is never torn down.
func startTestLinks(cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter) ([]cluster.LinkState, func(), error) {
	type created struct {
		cluster string
		rt      config.RuntimeConfig
	}
	var made []created
	teardown := func() {
		for i := len(made) - 1; i >= 0; i-- {
			if err := runtime.DestroyEnvironment(made[i].cluster, made[i].rt, rep); err != nil {
				rep.Warn("could not destroy linked test lab %s: %s", made[i].cluster, err)
			}
		}
	}

	var states []cluster.LinkState
	for _, l := range cfg.Links {
		ll, err := resolveLink(l, baseDir, flags)
		if err != nil {
			return nil, teardown, err
		}
		if ll.cfg == nil {
			if !kindClusterExists(ll.cluster) {
				return nil, teardown, fmt.Errorf("link '%s': lab %s isn't running — start it, or link to its config path so astrona test can start a copy", l.Name, ll.cluster)
			}
			states = append(states, cluster.LinkState{Name: l.Name, Cluster: ll.cluster})
			continue
		}
		if len(ll.cfg.Links) > 0 {
			return nil, teardown, fmt.Errorf("link '%s': %s has links of its own — astrona test doesn't start nested links yet", l.Name, ll.cfg.Metadata.Name)
		}
		if err := validateLabForRun(ll.cfg); err != nil {
			return nil, teardown, fmt.Errorf("link '%s' (%s): %w", l.Name, ll.cfg.Metadata.Name, err)
		}
		if err := requireTrust(ll.flags, ll.cfg, ll.baseDir); err != nil {
			return nil, teardown, fmt.Errorf("link '%s': %w", l.Name, err)
		}
		name := config.NormalizeTestClusterName(ll.cfg.Metadata.Name)
		if err := runtime.DestroyEnvironment(name, ll.cfg.Runtime, rep); err != nil {
			rep.Warn("could not clean up a previous '%s', proceeding anyway: %s", name, err)
		}
		made = append(made, created{name, ll.cfg.Runtime})
		rep.Section("Linked lab '%s' (test copy)", l.Name)
		if _, _, err := upLab(ll.cfg, ll.baseDir, name, nil, true, rep); err != nil {
			return nil, teardown, fmt.Errorf("linked lab '%s' (%s): %w", l.Name, name, err)
		}
		states = append(states, cluster.LinkState{Name: l.Name, Cluster: name})
	}
	return states, teardown, nil
}

// waitForClusterDNS blocks until CoreDNS can answer, so bootstrap scripts
// that resolve names right away — a linked lab's host, a service — don't
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
