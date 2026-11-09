package labops

import (
	"fmt"
	"io"
	"path/filepath"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/diagnostics"
	"astrona/internal/hypervisor"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// maxPrintedProblems bounds the unhealthy-pod lines echoed to the
// terminal/CI log; summary.md has them all.
const maxPrintedProblems = 10

// CollectDiagnostics gathers env's bundle into dir (default
// ~/.astrona/diagnostics/<lab>-<stamp>) and writes where it went plus the
// unhealthy pods to w (stderr in cmd). Never fails the caller —
// diagnostics are best effort on top of whatever already happened.
func CollectDiagnostics(w io.Writer, env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName, dir string, rep *ui.Reporter) {
	if dir == "" {
		d, err := diagnostics.DefaultDir(clusterName)
		if err != nil {
			rep.Warn("could not collect diagnostics: %s", err)
			return
		}
		dir = d
	}

	var sum diagnostics.Summary
	var err error
	switch env.Type {
	case runtime.RuntimeKind:
		sum, err = diagnostics.CollectKind(diagnostics.Kind{Name: clusterName, KubeContext: env.KubeContext, Kubeconfig: env.Kubeconfig}, dir, rep)
	case runtime.RuntimeQEMU:
		sum, err = diagnostics.CollectQEMU(clusterName, QEMUConsoleLogs(cfg, clusterName), dir, rep)
	default:
		return
	}
	if err != nil {
		rep.Warn("could not collect diagnostics: %s", err)
		return
	}
	linked := CollectLinkedDiagnostics(clusterName, cfg.KindClusters(), dir, rep)
	WriteDiagnosticsSummary(w, sum)
	WriteLinkedDiagnostics(w, linked)
}

// LinkedDiagnostics is one linked cluster's part of a bundle.
type LinkedDiagnostics struct {
	Cluster string
	Summary diagnostics.Summary
}

// CollectLinkedDiagnostics collects every linked cluster (runtime.kind.clusters)
// of the lab running as lab into dir/clusters/<cluster> — a failure there
// (the idp didn't come up) is often why the lab's own checks fail.
func CollectLinkedDiagnostics(lab string, labs []config.KindCluster, dir string, rep *ui.Reporter) []LinkedDiagnostics {
	var out []LinkedDiagnostics
	for _, c := range lifecycle.OwnedClusters(lab, labs) {
		if !cluster.Exists(c) {
			continue
		}
		sum, err := diagnostics.CollectKind(diagnostics.Kind{
			Name: c, KubeContext: "kind-" + c, Kubeconfig: cluster.ExistingKubeconfig(c),
		}, filepath.Join(dir, "clusters", c), rep)
		if err != nil {
			rep.Warn("could not collect diagnostics for linked cluster %s: %s", c, err)
			continue
		}
		out = append(out, LinkedDiagnostics{Cluster: c, Summary: sum})
	}
	return out
}

// collectRunLinkedFailure collects the linked clusters of a lab whose
// linked clusters failed to start, and writes them to w.
func collectRunLinkedFailure(w io.Writer, cfg *config.LabConfig, lab string, rep *ui.Reporter) {
	if len(cfg.KindClusters()) == 0 {
		return
	}
	dir, err := diagnostics.DefaultDir(lab)
	if err != nil {
		rep.Warn("could not collect linked-cluster diagnostics: %v", err)
		return
	}
	WriteLinkedDiagnostics(w, CollectLinkedDiagnostics(lab, cfg.KindClusters(), dir, rep))
}

// WriteLinkedDiagnostics writes where each linked cluster's bundle went
// and its unhealthy pods.
func WriteLinkedDiagnostics(w io.Writer, linked []LinkedDiagnostics) {
	for _, l := range linked {
		fmt.Fprintf(w, "\nLinked cluster %s: %s\n", l.Cluster, l.Summary.Dir)
		if len(l.Summary.Problems) == 0 {
			fmt.Fprintf(w, "  No unhealthy pods. %d warning event(s).\n", l.Summary.Warnings)
			continue
		}
		fmt.Fprintf(w, "  Unhealthy pods (%d):\n", len(l.Summary.Problems))
		for i, p := range l.Summary.Problems {
			if i == maxPrintedProblems {
				fmt.Fprintf(w, "    … %d more in %s/summary.md\n", len(l.Summary.Problems)-maxPrintedProblems, l.Summary.Dir)
				break
			}
			fmt.Fprintf(w, "    %s\n", p)
		}
	}
}

// QEMUConsoleLogs lists every VM of the lab under the names astrona list
// shows them as: the lab name for a single-VM lab, "<lab>-<vm>" otherwise.
func QEMUConsoleLogs(cfg *config.LabConfig, clusterName string) []diagnostics.QEMUVM {
	names := []string{clusterName}
	if config.IsMultiVM(cfg.Runtime.QEMU) {
		names = names[:0]
		for _, vm := range cfg.Runtime.QEMU {
			names = append(names, clusterName+"-"+vm.Name)
		}
	}
	var vms []diagnostics.QEMUVM
	for _, n := range names {
		if path, err := hypervisor.ConsoleLogPath(n); err == nil {
			vms = append(vms, diagnostics.QEMUVM{Name: n, ConsoleLog: path})
		}
	}
	return vms
}

// WriteDiagnosticsSummary writes where a bundle went, its suggested
// checks and its unhealthy pods.
func WriteDiagnosticsSummary(w io.Writer, sum diagnostics.Summary) {
	fmt.Fprintf(w, "\nDiagnostics bundle: %s\n", sum.Dir)
	for _, hint := range sum.Hints {
		fmt.Fprintf(w, "  Suggested check: %s\n", hint)
	}
	if len(sum.Problems) == 0 {
		fmt.Fprintf(w, "  No unhealthy pods. %d warning event(s) — see summary.md.\n", sum.Warnings)
		return
	}
	fmt.Fprintf(w, "  Unhealthy pods (%d):\n", len(sum.Problems))
	for i, p := range sum.Problems {
		if i == maxPrintedProblems {
			fmt.Fprintf(w, "    … %d more in summary.md\n", len(sum.Problems)-maxPrintedProblems)
			break
		}
		fmt.Fprintf(w, "    %s\n", p)
	}
	fmt.Fprintf(w, "  %d warning event(s) — see summary.md.\n", sum.Warnings)
}

// DiagnoseResult is what `astrona diagnose` collected, for cmd to print.
type DiagnoseResult struct {
	Summary diagnostics.Summary
	Linked  []LinkedDiagnostics
}

// Diagnose collects the running lab's bundle into outDir (default
// ~/.astrona/diagnostics/<lab>-<stamp>): the serial console of a qemu lab,
// else the kind cluster lab, plus each of its linked clusters. lab is
// already resolved by the caller. Progress goes to rep.
func Diagnose(lab, outDir string, rep *ui.Reporter) (*DiagnoseResult, error) {
	var err error
	if outDir == "" {
		if outDir, err = diagnostics.DefaultDir(lab); err != nil {
			return nil, err
		}
	}

	var sum diagnostics.Summary
	if hypervisor.StateExists(lab) {
		path, err := hypervisor.ConsoleLogPath(lab)
		if err != nil {
			return nil, err
		}
		sum, err = diagnostics.CollectQEMU(lab, []diagnostics.QEMUVM{{Name: lab, ConsoleLog: path}}, outDir, rep)
		if err != nil {
			return nil, err
		}
	} else {
		sum, err = diagnostics.CollectKind(diagnostics.Kind{
			Name: lab, KubeContext: "kind-" + lab, Kubeconfig: cluster.ExistingKubeconfig(lab),
		}, outDir, rep)
		if err != nil {
			return nil, err
		}
	}
	return &DiagnoseResult{Summary: sum, Linked: CollectLinkedDiagnostics(lab, nil, outDir, rep)}, nil
}
