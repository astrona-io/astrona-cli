package lifecycle

import (
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/executor"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/ui"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// softResetKinds are the namespaced kinds a soft reset clears out of the
// default namespace (which can't be deleted) — what a student typically
// creates there.
var softResetKinds = "deployments,statefulsets,daemonsets,replicasets,jobs,cronjobs,pods,services,configmaps,secrets," +
	"persistentvolumeclaims,ingresses,serviceaccounts,roles,rolebindings,networkpolicies"

// recordBaseline saves which namespaces exist before the lab's bootstrap
// (kind only).
func recordBaseline(env *runtime.LabEnvironment) error {
	if env.Type != runtime.RuntimeKind {
		return nil
	}
	nss, err := namespaces(env)
	if err != nil {
		return err
	}
	return cluster.WriteBaseline(env.Name, cluster.Baseline{Namespaces: nss})
}

func namespaces(env *runtime.LabEnvironment) ([]string, error) {
	out, err := kubectlOut(env, "get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func kubectlOut(env *runtime.LabEnvironment, args ...string) (string, error) {
	cmd := exec.Command("kubectl", append([]string{"--context", env.KubeContext}, args...)...)
	cmd.Env = executor.KubeconfigEnv(env.Kubeconfig)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// labNamespaces are the namespaces a soft reset deletes: everything not in
// the baseline. default is never one of them.
func labNamespaces(current, baseline []string) []string {
	var out []string
	for _, ns := range current {
		if ns != "default" && !slices.Contains(baseline, ns) {
			out = append(out, ns)
		}
	}
	return out
}

// softResetCluster puts one cluster back to its just-bootstrapped state
// without recreating it: deletes the namespaces created after its
// baseline, clears what isn't astrona's from default, and runs its
// bootstrap again.
func softResetCluster(cfg *config.LabConfig, baseDir string, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	base, ok, err := cluster.ReadBaseline(env.Name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s has no baseline (created by an older astrona) — use `astrona reset` without --soft", env.Name)
	}

	t := rep.Step("Remove the lab's namespaces in %s", env.Name)
	current, err := namespaces(env)
	if err != nil {
		return t.Fail(err)
	}
	if del := labNamespaces(current, base.Namespaces); len(del) > 0 {
		args := append([]string{"delete", "namespace", "--wait=true", "--timeout=3m"}, del...)
		if _, err := kubectlOut(env, args...); err != nil {
			return t.Fail(err)
		}
	}
	t.Done()

	t = rep.Step("Clear default namespace in %s", env.Name)
	// Everything astrona put there (astrona-clusters, astrona-ca, …) is
	// labelled and kept, as are Kubernetes' own objects.
	if _, err := kubectlOut(env, "delete", softResetKinds, "-n", "default", "--ignore-not-found",
		"-l", "app.kubernetes.io/managed-by!=astrona",
		"--field-selector", "metadata.name!=kubernetes,metadata.name!=kube-root-ca.crt,metadata.name!=default",
		"--wait=true", "--timeout=2m"); err != nil {
		return t.Fail(err)
	}
	t.Done()

	if err := Bootstrap(cfg, baseDir, env, rep); err != nil {
		return fmt.Errorf("bootstrap after the soft reset failed — `astrona reset` (without --soft) starts over completely: %w", err)
	}
	return nil
}

// SoftReset soft-resets the running lab clusterName: each linked
// cluster in start order (or only the one named), then the lab's own —
// and restarts the lab's port forwards, whose targets were recreated.
func SoftReset(cfg *config.LabConfig, baseDir, clusterName, only string, rep *ui.Reporter) ([]portforward.Forward, error) {
	if err := softResetClusters(cfg, baseDir, clusterName, only, rep); err != nil {
		return nil, err
	}
	if len(cfg.Runtime.PortForwards) == 0 {
		return nil, nil
	}
	rep.Section("Port forwards")
	forwards, err := StartPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
	if err != nil {
		rep.Warn("some port forwards could not be restarted — `astrona port-forward start`")
	}
	return forwards, nil
}

func softResetClusters(cfg *config.LabConfig, baseDir, clusterName, only string, rep *ui.Reporter) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != string(runtime.RuntimeKind) {
		return fmt.Errorf("reset --soft supports kind labs only — use `astrona reset`")
	}
	if !cluster.Exists(clusterName) {
		return fmt.Errorf("lab %s isn't running — `astrona run` creates it", clusterName)
	}
	order, states, err := ClusterStates(cfg, clusterName)
	if err != nil {
		return err
	}
	if only != "" {
		if _, err := FindLinkedCluster(clusterName, only, states); err != nil {
			return err
		}
	}
	byName := map[string]cluster.LinkState{}
	for _, s := range states {
		byName[s.Name] = s
	}
	for i, l := range order {
		if only != "" && l.Name != only {
			continue
		}
		sub := LinkedClusterConfig(cfg, l)
		env, err := runtime.LoadEnvironment(states[i].Cluster, sub.Runtime)
		if err != nil {
			return fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
		var deps []cluster.LinkState
		for _, d := range l.DependsOn {
			deps = append(deps, byName[d])
		}
		env.WithLinks(deps)
		env.AddEnv(CAEnv(clusterName)...)
		rep.Section("Linked cluster '%s'", l.Name)
		if err := softResetCluster(sub, baseDir, env, rep); err != nil {
			return fmt.Errorf("linked cluster '%s': %w", l.Name, err)
		}
	}
	if only != "" {
		return nil
	}

	env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime)
	if err != nil {
		return err
	}
	links, _ := Links(clusterName)
	env.WithLinks(links)
	env.AddEnv(CAEnv(clusterName)...)
	rep.Section("Lab: %s", cfg.Metadata.Name)
	if err := softResetCluster(cfg, baseDir, env, rep); err != nil {
		return err
	}
	// Like a full reset: the exam starts over.
	if cfg.Exam.Enabled() {
		if err := exam.Start(clusterName, cfg.Exam.Limit(), time.Now()); err != nil {
			rep.Warn("could not restart the exam clock: %s", err)
		}
	}
	return nil
}
