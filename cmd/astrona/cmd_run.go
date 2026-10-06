package main

import (
	"fmt"
	"os"
	"time"

	"astrona/internal/addons"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests). flags
// is bound to the root command's persistent --config/--file/--git/--git-ref
// flags.
func newRunCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [lab]",
		Short: "Start a lab: create its cluster(s) or VM(s) and set it up",
		Long: "Spin up a lab environment: create the kind cluster or qemu VM(s), run bootstrap init scripts, " +
			"and apply bootstrap manifests.\n\n" +
			"For a kind lab with runtime.portForwards, the forwards are started last (bound to 127.0.0.1) " +
			"and their URLs and status are printed when the lab is ready — see `astrona port-forward`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			useLabArg(args, flags) // a lab given as the argument wins over `astrona use`
			if flags.configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}

			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}

			rep, err := ui.NewReporter("run", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			if err := lifecycle.Validate(cfg); err != nil {
				return err
			}
			if err := validateParallel(flags.parallel); err != nil {
				return err
			}
			return bringUpLab(cfg, baseDir, flags, rep)
		},
	}

	addParallelFlag(cmd, flags)
	return cmd
}

// addParallelFlag adds --parallel to a command that creates a lab.
func addParallelFlag(cmd *cobra.Command, flags *rootFlags) {
	cmd.Flags().IntVar(&flags.parallel, "parallel", 1, fmt.Sprintf("Create up to N linked clusters (runtime.kind.clusters) at once, 1-%d (default 1: one at a time). dependsOn is still respected, and after a failure nothing new starts", lifecycle.MaxParallel))
}

// validateParallel checks --parallel.
func validateParallel(n int) error {
	if n < 1 || n > lifecycle.MaxParallel {
		return fmt.Errorf("--parallel must be between 1 and %d", lifecycle.MaxParallel)
	}
	return nil
}

// printConnectHints prints how to reach the lab: for a kind lab, the
// isolated-kubeconfig shell plus the context name (astrona never switches
// the user's own current-context); for a qemu lab, the paste-ready
// `astrona ssh` command for each VM.
func printConnectHints(env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName string) {
	if env.Type == runtime.RuntimeKind {
		fmt.Printf("\nConnect (your own kubectl current-context is unchanged):\n")
		fmt.Printf("    astrona shell %s                  # shell with kubectl pointed at the lab\n", clusterName)
		fmt.Printf("    kubectl --context %s ...     # or from any terminal\n", env.KubeContext)
		if env.Kubeconfig != "" {
			fmt.Printf("    export KUBECONFIG=%s\n", env.Kubeconfig)
		}
		if k := cfg.Runtime.Kind; k != nil && k.Addons.GatewayAPI != "" {
			p := k.Addons.EffectiveGatewayPorts()
			fmt.Printf("\nGateway API (GatewayClass %q, listeners on port 80/443, bound to 127.0.0.1 only):\n", addons.GatewayClassName)
			fmt.Printf("    http://<host>.localtest.me:%d    https://<host>.localtest.me:%d\n", p.HTTP, p.HTTPS)
		}
		return
	}
	if env.Type != runtime.RuntimeQEMU {
		return
	}

	// The name a VM is addressed under: the lab name for a single-VM lab,
	// "<lab>-<vm>" for each VM of a multi-VM lab — exactly what `astrona ssh`
	// / `astrona list` expect.
	fmt.Printf("\nConnect:\n")
	if config.IsMultiVM(cfg.Runtime.QEMU) {
		for _, vm := range cfg.Runtime.QEMU {
			fmt.Printf("    astrona ssh %s-%s\n", clusterName, vm.Name)
		}
		return
	}
	fmt.Printf("    astrona ssh %s\n", clusterName)
}

// bringUpLab creates cfg's linked clusters (runtime.kind.clusters), then the lab
// itself with everything `astrona run` does — preload, addons, bootstrap,
// manifests, readiness gates, port forwards — and prints how to connect.
// Shared by run and reset.
func bringUpLab(cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter) error {
	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
	// Checked before linked clusters are (re)created — they'd otherwise be
	// replaced under a lab that's still using them.
	if len(cfg.KindClusters()) > 0 && kindClusterExists(clusterName) {
		return fmt.Errorf("lab %s is already running — `astrona reset` starts it over", clusterName)
	}
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil {
		return err
	}
	links, err := lifecycle.StartLinkedClusters(cfg, baseDir, clusterName, false, flags.parallel, rep)
	if err != nil {
		return err
	}
	env, forwards, err := lifecycle.Up(cfg, baseDir, clusterName, links, false, rep)
	if err != nil {
		return err
	}

	// The clock starts once the lab is ready — setup time doesn't count.
	if cfg.Exam.Enabled() {
		if err := exam.Start(clusterName, cfg.Exam.Limit(), time.Now()); err != nil {
			rep.Warn("could not start the exam clock: %s", err)
		}
	}

	rep.Close()
	fmt.Printf("\nLab environment is fully loaded and ready!\n")
	printConnectHints(env, cfg, clusterName)
	printPortForwardHints(os.Stdout, forwards)
	printLinkHints(os.Stdout, links)
	if ca, ok := cluster.ExistingLabCA(clusterName); ok && cfg.Runtime.Kind != nil && cfg.Runtime.Kind.SharedCA {
		fmt.Printf("\nLab CA (trusted by every cluster via ConfigMap astrona-ca): %s\n    curl --cacert %s https://<name>.%s:<nodePort>/\n", ca.CertPath, ca.CertPath, cluster.LinkDomain)
	}
	if cfg.Exam.Enabled() {
		fmt.Printf("\nExam started — you have %s. `astrona submit` shows the time left.\n", exam.Round(cfg.Exam.Limit()))
	}
	if cfg.Metadata.Docs.ExamQuestion != "" {
		fmt.Printf("\nYour task: astrona docs question%s   (all docs: astrona docs)\n", configFlagHint(baseDir))
	}
	fmt.Printf("Full log: %s\n", rep.LogPath())
	return nil
}

// configFlagHint is the " -c <dir>" to append to a suggested command so it
// finds the same lab config — empty when that's the current directory.
func configFlagHint(baseDir string) string {
	if baseDir == "" || baseDir == "." {
		return ""
	}
	return " -c " + baseDir
}
