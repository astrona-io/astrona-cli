package main

import (
	"astrona/internal/account"
	"astrona/internal/apphome"
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"astrona/internal/addons"
	"astrona/internal/catalog"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/resources"
	"astrona/internal/runtime"
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
			"Yes accepts the displayed terms and settings and permits the lab's downloads and code for this invocation. No cancels startup. " +
			"If the lab already exists, the UI asks before destroying it and starting over with all work discarded. " +
			"That replacement stays local: no account session, progress or results are sent to Astrona. Use astrona ui to reopen without restarting. " +
			"Interactive runs without --ui offer the UI before downloads; Enter chooses Yes. Use --ui=false to keep the command-line flow. --yes, CI and redirected input/output skip the offer. The UI will become the default in v0.5.0.",
		Example: `  astrona run astrona.io/ATS014/section-010/module-01/lab-02   # catalog lab: no login needed
  astrona run ./labs/my-lab
  astrona run ./labs/my-lab --ui                      # terminal workspace
  astrona run --git https://github.com/org/labs -c labs/net-01
  astrona run renew                                  # a running playground: its full time limit again`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			exists := labExists(cfg, clusterName)
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
				choice, err = chooseRun(os.Stdin, promptOut, isatty.IsTerminal(os.Stdin.Fd()), yes,
					exists, label, otherRunningLabNames(clusterName))
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

			rep, err := ui.NewReporter("run", cfg.Metadata.Name, flags.verbose)
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
					flags.uiRestart = &uiRunRestart{clusterName, workspaceRunID(st)}
				}
				if err := discardUIRun(clusterName, flags.uiRestart); err != nil {
					return err
				}
			} else {
				endOldSession(clusterName) // an old playground's watchdog would remove the new lab
			}
			if choice.startOver {
				if flags.keepContext { // not switching again: put back what the old lab switched away from
					releaseLab(os.Stdout, clusterName)
				}
				teardown := cfg.Teardown
				teardown.KeepCluster = false
				info := teardownInfo{clusterName: clusterName, teardown: teardown, runtime: cfg.Runtime, skipScripts: flags.localOnly}
				if err := tearDownLabEnvironment(clusterName, info, baseDir, true, rep); err != nil {
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
	if n < 1 || n > lifecycle.MaxParallel {
		return fmt.Errorf("--parallel must be between 1 and %d", lifecycle.MaxParallel)
	}
	return nil
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

// bringUpLab creates cfg's linked clusters (runtime.kind.clusters), then the lab
// itself with everything `astrona run` does — preload, addons, bootstrap,
// manifests, readiness gates, port forwards — and prints how to connect.
// Once it is ready, a kind lab's context becomes the user's kubectl
// current-context (unless --keep-context), and where the lab came from and
// its lab session (sess, nil for none) are remembered for submit and
// destroy. Shared by run and reset.
func bringUpLab(cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter, sess *labstate.Session) error {
	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
	if err := checkRunEnvironment(cfg, baseDir, rep); err != nil {
		return err
	}
	// Checked before linked clusters are (re)created — they'd otherwise be
	// replaced under a lab that's still using them.
	if len(cfg.KindClusters()) > 0 && kindClusterExists(clusterName) {
		return fmt.Errorf("lab %s is already running — `astrona reset` starts it over", clusterName)
	}
	// Resources are collected and copied before anything is built, so a
	// broken resources/ folder costs no cluster. Nothing in them runs.
	res, err := resources.Collect(cfg, baseDir)
	if err != nil {
		return err
	}
	resDir, err := resources.Snapshot(clusterName, baseDir, res)
	if err != nil {
		return fmt.Errorf("could not copy the lab's resources: %w", err)
	}
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil {
		return err
	}
	links, err := lifecycle.StartLinkedClusters(cfg, baseDir, clusterName, false, flags.parallel, rep)
	if err != nil {
		collectRunLinkedFailure(cfg, clusterName, rep)
		return err
	}
	env, forwards, err := lifecycle.Up(cfg, baseDir, clusterName, links, false, rep)
	if err != nil {
		if env != nil {
			collectDiagnostics(env, cfg, clusterName, "", rep)
		}
		return err
	}

	// The clock starts once the lab is ready — setup time doesn't count.
	if cfg.Exam.Enabled() {
		if err := exam.Start(clusterName, cfg.Exam.Limit(), time.Now()); err != nil {
			rep.Warn("could not start the exam clock: %s", err)
		}
	}

	rememberLab(clusterName, flags, sess)
	rep.Close()
	var sw *labstate.Switched
	if env.Type == runtime.RuntimeKind && !flags.keepContext {
		sw = switchToLab(clusterName, env.KubeContext)
	}
	fmt.Printf("\nLab environment is fully loaded and ready!\n")
	printConnectHints(env, cfg, clusterName, sw)
	printPortForwardHints(os.Stdout, forwards)
	printLinkHints(os.Stdout, links)
	if ca, ok := cluster.ExistingLabCA(clusterName); ok && cfg.Runtime.Kind != nil && cfg.Runtime.Kind.SharedCA {
		fmt.Printf("\nLab CA (trusted by every cluster via ConfigMap astrona-ca): %s\n    curl --cacert %s https://<name>.%s:<nodePort>/\n", ca.CertPath, ca.CertPath, cluster.LinkDomain)
	}
	if cfg.Exam.Enabled() {
		fmt.Printf("\nExam started — you have %s. `astrona submit` shows the time left.\n", exam.Round(cfg.Exam.Limit()))
	}
	printResources(os.Stdout, resDir, res)
	if cfg.Metadata.Docs.Question != "" {
		fmt.Printf("\nYour task: astrona docs question%s   (all docs: astrona docs)\n", configFlagHint(baseDir))
	}
	fmt.Printf("Full log: %s\n", rep.LogPath())
	return nil
}

// labStart is the lab session a run or a full reset builds a lab under —
// none for a lab that isn't from the catalog. Shared by run and reset, so
// a reset ends exactly like a fresh run.
type labStart struct {
	page  *url.URL
	sess  *labstate.Session
	ready bool
}

// startLabSession starts acct's session for the catalog lab (nil acct:
// none) before anything is built — a timed playground for a module's
// playground, with the config's time limit.
func startLabSession(ctx context.Context, acct *labAccount, command string, cfg *config.LabConfig, lab string) (*labStart, error) {
	ls := &labStart{}
	if acct == nil {
		return ls, nil
	}
	var opts []account.SessionOptions
	if isPlaygroundName(lab) {
		minutes, _ := cfg.Metadata.TimeLimitMinutes() // checked by lifecycle.Validate
		opts = append(opts, account.SessionOptions{Kind: "playground", TimeLimitMinutes: minutes})
	}
	var err error
	ls.page, ls.sess, err = acct.startSession(ctx, command, lab, opts...)
	return ls, err
}

// abandon stops a playground's clock when the playground never became
// ready (deferred right after startLabSession) — it started with the
// session and would otherwise run on to the deadline with nothing to show
// for it. A Lab has no timer until its course page connects.
func (ls *labStart) abandon() {
	if ls.ready {
		return
	}
	ls.progress("failed", "Lab setup stopped before it was ready")
	if !ls.sess.IsPlayground() {
		return
	}
	stopPlaygroundClock(context.Background(), ls.sess, "destroyed")
}

// done follows a lab that is ready: a playground's watchdog starts (its
// clock is already running), and a Lab reports that the course page may connect.
func (ls *labStart) done(clusterName string) {
	ls.ready = true
	ls.progress("ready", "Lab environment is ready")
	if ls.sess.IsPlayground() {
		startPlaygroundWatchdog(clusterName, ls.sess)
		return
	}
	if ls.page != nil {
		fmt.Printf("\nLab ready. Continue in your course page, or open: %s\n", ls.page)
		printTimeLimit(ls.sess)
	}
}

func (ls *labStart) progress(phase, message string) {
	if ls == nil || ls.sess == nil || ls.sess.IsPlayground() {
		return
	}
	client, store, _, err := accountDeps()
	if err != nil {
		return
	}
	creds, err := client.Active(context.Background(), store, ls.sess.Site)
	if err != nil || creds == nil {
		return
	}
	_ = client.UpdateLabSessionProgress(context.Background(), creds, ls.sess.ID, phase, message)
}

// configFlagHint is the " -c <dir>" to append to a suggested command so it
// finds the same lab config — empty when that's the current directory.
func configFlagHint(baseDir string) string {
	if baseDir == "" || baseDir == "." {
		return ""
	}
	return " -c " + baseDir
}
