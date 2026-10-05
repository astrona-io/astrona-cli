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
		Short: "Spin up a lab environment",
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
			return bringUpLab(cfg, baseDir, rep)
		},
	}

	return cmd
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
	if err := config.ValidateKindConfig(cfg.Runtime); err != nil {
		return err
	}
	if cfg.Runtime.Type == string(runtime.RuntimeQEMU) {
		if err := config.ValidateQEMUVMs(cfg.Runtime.QEMU); err != nil {
			return err
		}
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

// bringUpLab creates cfg's environment and runs everything `astrona run`
// does on top — preload, addons, bootstrap, manifests, readiness gates,
// port forwards — then prints how to connect. Shared by run and reset.
func bringUpLab(cfg *config.LabConfig, baseDir string, rep *ui.Reporter) error {
	rep.Section("Lab: %s", cfg.Metadata.Name)

	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)

	env, err := runtime.CreateEnvironment(clusterName, baseDir, cfg.Runtime, rep)
	if err != nil {
		return fmt.Errorf("lab setup failed: %w", err)
	}

	// Before addons and bootstrap, so anything they start can use
	// the preloaded images.
	if images := labPreloadImages(cfg, baseDir); len(images) > 0 {
		rep.Section("Images")
		if err := cluster.PreloadImages(clusterName, images, rep); err != nil {
			return fmt.Errorf("image preload failed: %w", err)
		}
	}

	if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
		rep.Section("Addons")
		if err := addons.Install(k.Addons, env.KubeContext, rep); err != nil {
			return fmt.Errorf("addons failed: %w", err)
		}
	}

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

	// Started last, once manifests are applied and readiness gates
	// passed, so there's something to forward to. A forward that
	// fails or isn't ready yet never fails the run — the lab itself
	// is up, and the supervisor keeps retrying.
	var forwards []portforward.Forward
	if len(cfg.Runtime.PortForwards) > 0 {
		rep.Section("Port forwards")
		forwards, err = startLabPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
		if err != nil {
			rep.Warn("some port forwards could not be started — fix and retry with `astrona port-forward start -c <config>`")
		}
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
