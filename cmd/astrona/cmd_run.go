package main

import (
	"astrona/internal/apphome"
	"context"
	"fmt"
	"os"

	"astrona/internal/addons"
	"astrona/internal/catalog"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/settings"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests). flags
// is bound to the root command's persistent --config/--file/--git/--git-ref
// flags.
func newRunCmd(flags *rootFlags) *cobra.Command {
	var yes, withoutLogin, workspace, uiStartup bool
	cmd := &cobra.Command{
		Use:   "run [lab]",
		Short: "Start a lab: create its cluster(s) or VM(s) and set it up",
		Long: "Spin up a lab environment: create the kind cluster or qemu VM(s), run bootstrap init scripts, " +
			"and apply bootstrap manifests.\n\n" +
			"Before creating Kind clusters, check the engine, resources and host ports. Memory estimates include " +
			"running Kind clusters created outside Astrona. Estimates warn; definite environment failures block startup. " +
			"On startup failure, collect diagnostics under ~/" + apphome.Name() + "/diagnostics. Failed Kind creation exports " +
			"retained node logs before cleaning up that new cluster; later failures leave the lab available for diagnosis. " +
			"CoreDNS readiness defaults to 3m; runtime.kind.dnsTimeout can override it.\n\n" +
			"Once a kind lab is ready, kubectl points at it: its context (kind-<lab>) becomes the " +
			"current-context of your kubeconfig ($KUBECONFIG is respected), and `astrona destroy` " +
			"switches back to the context you had before — unless you've switched elsewhere " +
			"meanwhile. --keep-context leaves your current-context alone. qemu labs are reached " +
			"with `astrona ssh`.\n\n" +
			"For a kind lab with runtime.portForwards, the forwards are started last (bound to 127.0.0.1) " +
			"and their URLs and status are printed when the lab is ready — see `astrona port-forward`.\n\n" +
			"A lab from the catalog (`astrona run astrona.io/ATS014/section-010/module-01/lab-02`, see `astrona labs`) " +
			"offers login when you are not signed in: progress and results will not be tracked on Astrona as a guest. " +
			"Choose Log in or Continue as guest; Never ask again saves login-reminder=no. " +
			"Use --without-login to confirm in automation. If you sign in with `astrona login`, before anything is built " +
			"astrona starts a lab session on Astrona; once the lab is ready it prints the lab page URL. " +
			"Your matching course page connects to it without a redirect, and setup time never counts. Labs from " +
			"your own files or repositories (-c / --git) run without signing in.\n\n" +
			"If the lab is already running, run asks whether to destroy it and start over (or keep it as " +
			"it is); --yes starts over without asking and is required when not in a terminal. If other " +
			"labs are running, it also offers to destroy them first — only when asked in a terminal; " +
			"--yes never touches other labs.\n\n" +
			"--ui shows one startup notice with Yes/No choices, then opens a lab's terminal workspace once it is ready. " +
			"Downloads, setup, errors and interactive shells stay inside the UI's terminal pane. " +
			"Yes accepts the displayed terms and settings and starts setup. It is not code trust: a remote lab still shows its trust prompt, and another astrona release is only installed after its own prompt (or --trust / --install-version). No cancels startup. " +
			"If the lab already exists, the UI asks before destroying it and starting over with all work discarded. " +
			"That replacement stays local: no account session, progress or results are sent to Astrona. Use astrona ui to reopen without restarting. " +
			"Interactive runs without --ui offer the UI before downloads; Enter chooses Yes. Use --ui=false to keep the command-line flow. --yes, CI and redirected input/output skip the offer. In v0.5.0 the terminal UI is replaced by a browser workspace.\n\n" +
			"--headless keeps Kind and QEMU setup headless, including in v0.5.0 and later. It skips the UI, login reminder and new account sessions, " +
			"then returns after setup with the lab still running for external validation. Missing approvals fail instead of prompting, even in a terminal. " +
			"Terms, settings review, remote-code trust and --yes for replacing an existing run still apply. This uses the normal lab name and setup; " +
			"it does not grade or clean up automatically. Use astrona destroy when finished, or astrona test for the full setup, reference solution, grading and cleanup pipeline.",
		Example: `  astrona run astrona.io/ATS014/section-010/module-01/lab-02   # catalog lab: no login needed
  astrona run ./labs/my-lab
  astrona run ./labs/my-lab --ui                      # terminal workspace
  astrona run ./labs/my-lab --headless --accept-terms # setup for external checks
  astrona run --git https://github.com/org/labs -c labs/net-01
  astrona run renew                                  # a running playground: its full time limit again`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := configureHeadless(cmd); err != nil {
				return err
			}
			if workspace || uiStartup {
				if err := requireWorkspaceTerminal(); err != nil {
					return err
				}
				if os.Getenv(labShellEnvVar) != "" {
					flags.keepContext = true
				}
			}
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			if flags.configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()
			if workspace || uiStartup {
				if err := workspaceRuntime(cfg); err != nil {
					return err
				}
			}

			// Resolve restart consent before account access or runtime changes.
			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
			release, err := labstate.LockMutation(clusterName)
			if err != nil {
				return err
			}
			defer release()
			exists, err := labops.LabExists(cfg, clusterName)
			if err != nil {
				return err
			}
			label := clusterName
			if len(args) > 0 && catalog.LooksLikeLabID(args[0]) {
				label = args[0] + " (" + clusterName + ")"
			}
			var choice runChoice
			if workspace || uiStartup {
				if exists {
					if err := approveUIRestart(cmd, flags, clusterName, confirmUIRun); err != nil {
						return err
					}
					choice.startOver = true
				} else if flags.uiRestart != nil {
					return fmt.Errorf("the lab changed after the restart notice; nothing was started. Run the command again")
				}
			} else {
				choice, err = chooseRun(os.Stdin, promptOut, !flags.headless && isatty.IsTerminal(os.Stdin.Fd()), yes,
					exists, label, labops.OtherRunningLabNames(clusterName))
			}
			if err != nil {
				return err
			}
			if choice.keep {
				fmt.Printf("Kept the running lab %s — nothing changed.\n", clusterName)
				return nil
			}

			ctx := cmd.Context()
			// Local restarts skip credentials, renewal and account sessions.
			acct, err := accountForLab(ctx, flags)
			if err != nil {
				return err
			}

			approved := true
			if !flags.localOnly {
				acct, approved, err = confirmGuestLabRun(cmd, flags, acct, withoutLogin)
			}
			if err != nil {
				return err
			}
			if !approved {
				fmt.Println("Cancelled — sign in with `astrona login` to track your progress, then try again.")
				return nil
			}

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}
			if err := lifecycle.Validate(cfg); err != nil {
				return err
			}
			if err := validateParallel(flags.parallel); err != nil {
				return err
			}

			// The session is started before the build, so a refusal costs
			// nothing; the page is only opened once the lab is ready.
			start, err := startLabSession(ctx, acct, "run", cfg, flags.catalogLab)
			if err != nil {
				return err
			}
			defer start.abandon()
			start.progress("preparing", "Preparing your lab environment")

			rep, err := ui.New("run", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			for _, other := range choice.stopOthers {
				rep.Section("Destroy %s", other)
				if err := destroyByName(other, rep); err != nil {
					rep.Warn("couldn't destroy %s: %s — continuing", other, err)
				}
			}
			if flags.localOnly {
				if !choice.startOver {
					st, err := labstate.Load(clusterName)
					if err != nil {
						return err
					}
					flags.uiRestart = &uiRunRestart{clusterName, labops.RunID(st)}
				}
				if err := discardUIRun(clusterName, flags.uiRestart); err != nil {
					return err
				}
			} else {
				endOldSession(clusterName) // an old playground's watchdog would remove the new lab
			}
			if choice.startOver {
				// With --keep-context run won't switch again: the old lab's context goes back first.
				if err := labops.RemoveForRestart(os.Stdout, cfg, baseDir, clusterName, flags.keepContext, flags.localOnly, rep); err != nil {
					return fmt.Errorf("could not remove the running lab, nothing was started: %w", err)
				}
			}
			if err := bringUpLab(cfg, baseDir, flags, rep, start.sess); err != nil {
				return err
			}
			start.done(clusterName)
			release()
			if workspace {
				return runLabWorkspace(cmd.Context(), cfg, baseDir, flags)
			}
			return nil
		},
	}

	addParallelFlag(cmd, flags)
	addKeepContextFlag(cmd, flags)
	cmd.Flags().BoolVar(&workspace, "ui", false, "Open the terminal workspace (Kind or QEMU labs); confirm a local-only restart if the lab already exists")
	cmd.Flags().BoolVar(&flags.headless, "test-mode", false, "Deprecated alias for --headless")
	legacy := cmd.Flags().Lookup("test-mode")
	legacy.Hidden, legacy.Deprecated = true, "use --headless instead"
	cmd.Flags().BoolVar(&uiStartup, "ui-startup", false, "Confirm startup in the UI and return to Home")
	cmd.Flags().Lookup("ui-startup").Hidden = true
	cmd.Flags().BoolVar(&withoutLogin, "without-login", false, "Confirm running a catalog lab without signing in or tracking progress on Astrona")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "If the lab is already running, destroy it and start over without asking")
	cmd.AddCommand(newRunRenewCmd())
	return cmd
}

// addKeepContextFlag adds --keep-context to a command that creates a lab.
func addKeepContextFlag(cmd *cobra.Command, flags *rootFlags) {
	cmd.Flags().BoolVar(&flags.keepContext, "keep-context", false, "Leave your kubectl current-context alone (kind labs) — reach the lab with astrona shell or kubectl --context instead")
}

// addParallelFlag adds --parallel to a command that creates a lab.
func addParallelFlag(cmd *cobra.Command, flags *rootFlags) {
	cmd.Flags().IntVar(&flags.parallel, "parallel", 1, fmt.Sprintf("Create up to N linked clusters (runtime.kind.clusters) at once, 1-%d (default 1: one at a time). dependsOn is still respected, and after a failure nothing new starts", lifecycle.MaxParallel))
}

// validateParallel checks --parallel.
func validateParallel(n int) error {
	return settings.ValidateParallel(n)
}

// printConnectHints prints how to reach the lab: for a kind lab, either
// that kubectl now points at it (sw: run switched the user's
// current-context, and destroy switches it back) or — with --keep-context,
// or when the switch failed — the isolated-kubeconfig shell plus the
// context name; for a qemu lab, the paste-ready `astrona ssh` command for
// each VM.
func printConnectHints(env *runtime.LabEnvironment, cfg *config.LabConfig, clusterName string, sw *labstate.Switched) {
	if env.Type == runtime.RuntimeKind {
		if sw != nil {
			fmt.Printf("\nkubectl now points at this lab (%s).", env.KubeContext)
			if sw.AlreadyCurrent {
				fmt.Printf("\n")
			} else {
				fmt.Printf(" Your previous context %s comes back with: astrona destroy %s\n", previousContextLabel(sw), clusterName)
			}
			fmt.Printf("    kubectl get nodes\n")
			fmt.Printf("    astrona shell %s                  # or a shell with only this lab's kubeconfig\n", clusterName)
		} else {
			fmt.Printf("\nConnect (your own kubectl current-context is unchanged):\n")
			fmt.Printf("    astrona shell %s                  # shell with kubectl pointed at the lab\n", clusterName)
			fmt.Printf("    kubectl --context %s ...     # or from any terminal\n", env.KubeContext)
			if env.Kubeconfig != "" {
				fmt.Printf("    export KUBECONFIG=%s\n", env.Kubeconfig)
			}
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

// bringUpLab creates the lab (labops.Provision: preflight, linked
// clusters, the lab itself, the exam clock, remembering it, the kubectl
// switch) and prints how to connect. sess is its lab session (nil for
// none). Shared by run and reset.
func bringUpLab(cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter, sess *labstate.Session) error {
	res, err := labops.Provision(labops.ProvisionRequest{
		Config:       cfg,
		BaseDir:      baseDir,
		Source:       rememberedSource(flags),
		LocalOnly:    flags.localOnly,
		KeepContext:  flags.keepContext,
		Parallel:     flags.parallel,
		Session:      sess,
		ConfigChecks: labConfigResults,
		Warn:         os.Stderr,
	}, rep)
	if err != nil {
		return err
	}
	clusterName := res.ClusterName
	fmt.Printf("\nLab environment is fully loaded and ready!\n")
	printConnectHints(res.Env, cfg, clusterName, res.Switched)
	printPortForwardHints(os.Stdout, res.Forwards)
	printLinkHints(os.Stdout, res.Links)
	if ca, ok := cluster.ExistingLabCA(clusterName); ok && cfg.Runtime.Kind != nil && cfg.Runtime.Kind.SharedCA {
		fmt.Printf("\nLab CA (trusted by every cluster via ConfigMap astrona-ca): %s\n    curl --cacert %s https://<name>.%s:<nodePort>/\n", ca.CertPath, ca.CertPath, cluster.LinkDomain)
	}
	if res.ExamStarted {
		fmt.Printf("\nExam started — you have %s. `astrona submit` shows the time left.\n", exam.Round(cfg.Exam.Limit()))
	}
	printResources(os.Stdout, res.ResourcesDir, res.Resources)
	if cfg.Metadata.Docs.Question != "" {
		fmt.Printf("\nYour task: astrona docs question%s   (all docs: astrona docs)\n", configFlagHint(baseDir))
	}
	fmt.Printf("Full log: %s\n", res.LogPath)
	return nil
}

// labStart is the lab session a run or a full reset builds a lab under
// (labops.LabStart), under the names cmd's callers and tests use. S15
// removes it.
type labStart struct {
	ls   *labops.LabStart
	sess *labstate.Session
}

// startLabSession starts acct's session for the catalog lab (nil acct:
// none) before anything is built (labops.StartLabSession); it says so on
// stdout.
func startLabSession(ctx context.Context, acct *labAccount, command string, cfg *config.LabConfig, lab string) (*labStart, error) {
	ls, err := labops.StartLabSession(ctx, os.Stdout, os.Stderr, acct, command, cfg, lab)
	return &labStart{ls: ls, sess: ls.Session}, err
}

func (s *labStart) abandon()                       { s.ls.Abandon() }
func (s *labStart) done(clusterName string)        { s.ls.Done(clusterName) }
func (s *labStart) progress(phase, message string) { s.ls.Progress(phase, message) }

// configFlagHint is the " -c <dir>" to append to a suggested command so it
// finds the same lab config — empty when that's the current directory.
func configFlagHint(baseDir string) string {
	if baseDir == "" || baseDir == "." {
		return ""
	}
	return " -c " + baseDir
}
