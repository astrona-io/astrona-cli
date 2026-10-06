package main

import (
	"fmt"
	"os"
	"time"

	"astrona/internal/addons"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/manifests"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests). flags
// is bound to the root command's persistent --config/--file/--git/--git-ref
// flags.
func newRunCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start a lab: create its cluster(s) or VM(s) and set it up",
		Long: "Spin up a lab environment: create the kind cluster or qemu VM(s), run bootstrap init scripts, " +
			"and apply bootstrap manifests.\n\n" +
			"For a kind lab with runtime.portForwards, the forwards are started last (bound to 127.0.0.1) " +
			"and their URLs and status are printed when the lab is ready — see `astrona port-forward`.",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if err := validateLabForRun(cfg); err != nil {
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
	cmd.Flags().IntVar(&flags.parallel, "parallel", 1, fmt.Sprintf("Create up to N linked clusters (runtime.kind.clusters) at once, 1-%d (default 1: one at a time). dependsOn is still respected, and after a failure nothing new starts", maxParallel))
}

// validateParallel checks --parallel.
func validateParallel(n int) error {
	if n < 1 || n > maxParallel {
		return fmt.Errorf("--parallel must be between 1 and %d", maxParallel)
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

// validateLabForRun checks everything about cfg that can be checked
// before anything is created — a bad gate or forward entry is a config
// mistake, not something to discover after a 1-minute cluster boot (or,
// for `astrona reset`, after the old lab is already gone).
func validateLabForRun(cfg *config.LabConfig) error {
	if err := config.ValidateAPIVersion(cfg); err != nil {
		return err
	}
	if err := config.ValidateKindConfig(cfg.Runtime); err != nil {
		return err
	}
	if cfg.Runtime.Type == string(runtime.RuntimeQEMU) {
		if err := config.ValidateQEMUVMs(cfg.Runtime.QEMU); err != nil {
			return err
		}
	}
	if err := config.ValidateKindClusters(cfg); err != nil {
		return err
	}
	if err := config.ValidateExam(cfg); err != nil {
		return err
	}
	if err := config.ValidateChecks(cfg); err != nil {
		return err
	}
	if err := config.ValidateScoring(cfg); err != nil {
		return err
	}
	if err := config.ValidateWaitFor(cfg); err != nil {
		return err
	}
	if err := config.ValidatePortForwards(cfg.Runtime); err != nil {
		return err
	}
	return nil
}

// upLab creates cfg's environment under clusterName and runs everything
// `astrona run` does on top — preload, addons, lab CA, bootstrap,
// manifests, readiness gates, port forwards (not forTest) — with its
// linked clusters' addresses made available to it. The one pipeline for
// the lab itself (run, reset, test) and for each linked cluster. No
// summary output.
//
// Once the environment exists it's returned even when a later step fails,
// so the caller can collect diagnostics and tear it down (astrona test).
func upLab(cfg *config.LabConfig, baseDir, clusterName string, links []cluster.LinkState, forTest bool, rep *ui.Reporter) (*runtime.LabEnvironment, []portforward.Forward, error) {
	// A test copy of a lab mustn't fight its real `run` for host ports.
	if forTest && cfg.Runtime.Kind != nil {
		k := *cfg.Runtime.Kind
		k.Addons.SkipHostPorts = true
		cfg.Runtime.Kind = &k
	}
	rep.Section("Lab: %s", cfg.Metadata.Name)

	env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
	if err != nil {
		return nil, nil, fmt.Errorf("lab setup failed: %w", err)
	}
	if err := attachLinks(env, clusterName, links, rep); err != nil {
		return env, nil, err
	}

	// Before addons and bootstrap, so anything they start can use
	// the preloaded images.
	if k := cfg.Runtime.Kind; k != nil && len(k.PreloadImages) > 0 {
		rep.Section("Images")
		if err := cluster.PreloadImages(clusterName, k.PreloadImages, rep); err != nil {
			return env, nil, fmt.Errorf("image preload failed: %w", err)
		}
	}

	if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
		rep.Section("Addons")
		if err := addons.Install(k.Addons, env.KubeContext, rep); err != nil {
			return env, nil, fmt.Errorf("addons failed: %w", err)
		}
	}

	if err := installSharedCA(cfg, env, rep); err != nil {
		return env, nil, err
	}

	if err := waitForClusterDNS(cfg, env, rep); err != nil {
		return env, nil, fmt.Errorf("cluster DNS not ready: %w", err)
	}

	// What exists now is the platform (kube-system, addons, …); the soft
	// reset deletes every namespace created after it.
	if err := recordBaseline(env); err != nil {
		rep.Warn("could not record the lab's baseline (reset --soft won't work): %s", err)
	}
	if err := bootstrapLab(cfg, baseDir, env, rep); err != nil {
		return env, nil, err
	}

	// Started last, once manifests are applied and readiness gates
	// passed, so there's something to forward to. A forward that
	// fails or isn't ready yet never fails the run — the lab itself
	// is up, and the supervisor keeps retrying.
	var forwards []portforward.Forward
	if len(cfg.Runtime.PortForwards) > 0 && !forTest {
		rep.Section("Port forwards")
		forwards, err = startLabPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
		if err != nil {
			rep.Warn("some port forwards could not be started — fix and retry with `astrona port-forward start -c <config>`")
		}
	}
	return env, forwards, nil
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
	if err := prepareSharedCA(cfg, clusterName); err != nil {
		return err
	}
	links, err := startKindClusters(cfg, baseDir, clusterName, false, flags.parallel, rep)
	if err != nil {
		return err
	}
	env, forwards, err := upLab(cfg, baseDir, clusterName, links, false, rep)
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

// bootstrapLab runs cfg's bootstrap on env: init scripts, manifests, then
// readiness gates. Part of upLab; reset --soft runs it again.
func bootstrapLab(cfg *config.LabConfig, baseDir string, env *runtime.LabEnvironment, rep *ui.Reporter) error {
	if scripts.HasBootstrapInit(cfg) {
		rep.Section("Bootstrap")
		if err := scripts.RunBootstrap(cfg, baseDir, env, rep); err != nil {
			return fmt.Errorf("init scripts failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.Manifests) > 0 {
		if env.KubeContext == "" {
			return fmt.Errorf("bootstrap.manifests requires a kubectl-reachable cluster, but runtime '%s' has none", env.Type)
		}
		rep.Section("Manifests")
		if err := manifests.ApplyManifests(cfg.Bootstrap.Manifests, baseDir, env.KubeContext, rep); err != nil {
			return fmt.Errorf("bootstrap manifests failed: %w", err)
		}
	}

	if len(cfg.Bootstrap.WaitFor) > 0 {
		rep.Section("Readiness")
		if err := manifests.WaitFor(cfg.Bootstrap.WaitFor, env.KubeContext, rep); err != nil {
			return fmt.Errorf("lab did not become ready: %w", err)
		}
	}
	return nil
}
