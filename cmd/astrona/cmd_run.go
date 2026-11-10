package main

import (
	"astrona/internal/account"
	"astrona/internal/apphome"
	"astrona/internal/authoring"
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
	"astrona/internal/workspace"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// newRunCmd builds `astrona run`: create the lab environment (kind cluster
// or qemu VM), then run bootstrap (init scripts + apply manifests). flags
// is bound to the root command's persistent --config/--file/--git/--git-ref
// flags.
func newRunCmd(flags *rootFlags) *cobra.Command {
	var yes, withoutLogin, browser, noBrowser bool
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
			"Ctrl+C (or SIGTERM) while the lab is being built stops at the current step, removes what this " +
			"run created (its clusters or VMs, port forwards, the copy of its resources), closes the lab " +
			"session it started and exits 1. A second Ctrl+C quits at once; `astrona destroy` removes " +
			"whatever is left.\n\n" +
			"Once a kind lab is ready, kubectl points at it: its context (kind-<lab>) becomes the " +
			"current-context of your kubeconfig ($KUBECONFIG is respected), and `astrona destroy` " +
			"switches back to the context you had before — unless you've switched elsewhere " +
			"meanwhile. --keep-context leaves your current-context alone. qemu labs are reached " +
			"with `astrona ssh`.\n\n" +
			"For a kind lab with runtime.portForwards, the forwards are started last (bound to 127.0.0.1) " +
			"and their URLs and status are printed when the lab is ready — see `astrona port-forward`.\n\n" +
			"A lab from the catalog (`astrona run astrona.io/ATS014/section-010/module-01/lab-02`, see `astrona labs`) " +
			"offers login when you are not signed in: progress and results will not be tracked on Astrona as a guest. " +
			"Answer yes to log in, no to continue as a guest, or never to continue as a guest and save login-reminder=no. " +
			"Use --without-login to confirm in automation. If you sign in with `astrona login`, before anything is built " +
			"astrona starts a lab session on Astrona; once the lab is ready it prints the lab page URL. " +
			"Your matching course page connects to it without a redirect, and setup time never counts. Labs from " +
			"your own files or repositories (-c / --git) run without signing in.\n\n" +
			"While a signed-in lab runs, a background helper sends its CPU and memory use to the lab session every " +
			"15 seconds, and submit and playground deletion send a usage summary; `astrona settings set usage-samples no` turns " +
			"that off. --local-only keeps the whole run on this machine: astrona does not read your sign-in, starts no " +
			"lab session and sends no progress, results or usage, and `astrona submit` records the result locally only. " +
			"Starting a running lab over with --local-only detaches its session without closing it on Astrona.\n\n" +
			"If the lab is already running, run asks whether to destroy it and start over (or keep it as " +
			"it is); --yes starts over without asking and is required when not in a terminal. If other " +
			"labs are running, it also offers to destroy them first — only when asked in a terminal; " +
			"--yes never touches other labs.\n\n" +
			"An interactive run with the browser setting at always (the default) or --browser opens the browser first: astrona " +
			"starts the local browser workspace (or reuses a running one), prints its link and opens it. The tab shows the CLI " +
			"terms when they still need accepting (declining stops the command), then the lab being set up, then the lab. " +
			"Questions the terminal asks (start over?, log in?, trust?) are answered there. " +
			"The link has a single-use code that expires after 2 minutes; `astrona ui <lab>` opens a fresh one and " +
			"`astrona ui stop` stops the workspace (labs keep running). An exam's timer starts when you click Start in the browser, not when " +
			"the lab is ready. With `astrona settings set browser ask` the lab is set up in the terminal first, which then asks \"Open this lab in your browser?\" " +
			"(yes/no/always/never) each run, and never keeps runs in the terminal. --browser and --no-browser decide for one run. " +
			"Without a terminal on stdin and stdout, or with no browser on the machine (no display), the workspace is not opened " +
			"unless --browser or a saved always asks for it. --headless never opens the browser.\n\n" +
			"A QEMU (VM) lab opens the same way, with one terminal tab per VM, each the student's ssh login on that VM; " +
			"run also prints the astrona ssh command for each VM, and astrona status <lab> shows them.\n\n" +
			"--headless keeps Kind and QEMU setup headless. It skips the browser workspace, login reminder and new account sessions, " +
			"then returns after setup with the lab still running for external validation. Missing approvals fail instead of prompting, even in a terminal. " +
			"Terms, settings review, remote-code trust and --yes for replacing an existing run still apply. This uses the normal lab name and setup; " +
			"it does not grade or clean up automatically. Use astrona destroy when finished, or astrona test for the full setup, reference solution, grading and cleanup pipeline.",
		Example: `  astrona run astrona.io/ATS014/section-010/module-01/lab-02   # catalog lab: no login needed
  astrona run ./labs/my-lab
  astrona run ./labs/my-lab --browser                 # browser workspace
  astrona run ./labs/my-lab --headless --accept-terms # setup for external checks
  astrona run --git https://github.com/org/labs -c labs/net-01
  astrona run renew                                  # a running playground: its full time limit again`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			defer func() { flags.launch.finish(err) }()
			if err := configureHeadless(cmd); err != nil {
				return err
			}
			if err := checkBrowserFlags(browser, flags.headless); err != nil {
				return err
			}
			// Open the browser now, so the learner follows the setup there.
			// The terms gate may have opened it already.
			if flags.launch == nil && browserFirst(cmd, flags) {
				if l, err := startBrowserLaunch(workspace.LaunchChecking, "", nil); err != nil {
					fmt.Fprintf(os.Stderr, "Could not open the browser workspace yet (%v); it opens once the lab is ready.\n", err)
				} else {
					flags.launch = l
				}
			}
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			if flags.configPath == "" {
				return fmt.Errorf("please specify a configuration file using --config or -c")
			}
			if flags.adoptSession != "" && (flags.catalogLab == "" || flags.localOnly || flags.headless) {
				return fmt.Errorf("--session runs a lab session the website started: it needs an Astrona catalog lab, without --local-only or --headless")
			}
			cfg, baseDir, configCleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer configCleanup()
			if flags.launch != nil {
				flags.launch.SetTitle(labops.SanitizeLine(cfg.Metadata.Name))
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
			choice, err := chooseRun(os.Stdin, promptOut, !flags.headless && isatty.IsTerminal(os.Stdin.Fd()), yes,
				exists, label, labops.OtherRunningLabNames(clusterName))
			if err != nil {
				return err
			}
			if choice.keep {
				fmt.Printf("Kept the running lab %s — nothing changed.\n", clusterName)
				flags.launch.End(workspace.LaunchEnded, "The lab was already running and was kept as it is. Open it with: astrona ui "+uiLabArg(clusterName))
				return nil
			}

			ctx := cmd.Context()
			// Local restarts skip credentials, renewal and account sessions.
			acct, err := accountForLab(ctx, flags)
			if err != nil {
				return err
			}
			if flags.adoptSession != "" && acct == nil {
				return labops.NotSignedInError(account.ErrSignedOut, "run", flags.catalogLab)
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
				flags.launch.End(workspace.LaunchEnded, "Cancelled in the terminal: sign in with astrona login, then run the lab again.")
				return nil
			}

			if err := requireTrust(flags, cfg, baseDir); err != nil {
				return err
			}
			if err := lifecycle.Validate(cfg); err != nil {
				return err
			}
			if err := settings.ValidateParallel(flags.parallel); err != nil {
				return err
			}
			if flags.launch != nil {
				flags.browserRun = true // decided by browserFirst, and the tab is open
			} else if flags.browserRun, err = wantBrowser(flags, browser, noBrowser); err != nil {
				return err
			}

			// Every question is answered: from here Ctrl+C stops the build
			// and removes what this run created (a second one quits now).
			ctx, disarm := interruptible(cmd, interruptBuildNotice)
			defer disarm()

			// The session is started before the build, so a refusal costs
			// nothing; the page is only opened once the lab is ready.
			var start *labops.LabStart
			if flags.adoptSession != "" {
				start, err = labops.AdoptLabSession(ctx, os.Stdout, os.Stderr, acct, "run", cfg, flags.catalogLab, flags.adoptSession)
			} else {
				start, err = labops.StartLabSession(ctx, os.Stdout, os.Stderr, acct, "run", cfg, flags.catalogLab)
			}
			if err != nil {
				return err
			}
			defer start.Abandon()
			flags.runAccount = runAccountStatus(flags, acct, start.Session)
			flags.launch.SetAccount(flags.runAccount)
			start.Progress("preparing", "Preparing your lab environment")

			rep, err := ui.New("run", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()
			rep.SetObserver(start.Observe)
			if flags.launch != nil {
				rep.SetObserver(func(e ui.Event) {
					flags.launch.Observe(e)
					start.Observe(e)
				})
				flags.launch.SetLog(rep.LogPath())
				flags.launch.Stage(workspace.LaunchPreparing, "")
			}

			for _, other := range choice.stopOthers {
				rep.Section("Destroy %s", other)
				if err := labops.DestroyByName(other, rep, destroyOptions()); err != nil {
					rep.Warn("couldn't destroy %s: %s — continuing", other, err)
				}
			}
			if flags.localOnly {
				// --local-only: detach the old run here, never through the
				// Astrona API, before it is replaced.
				if err := detachRunLocally(clusterName); err != nil {
					return err
				}
			} else {
				labops.EndOldSession(os.Stderr, clusterName) // an old playground's watchdog would remove the new lab
			}
			if choice.startOver {
				// With --keep-context run won't switch again: the old lab's context goes back first.
				if err := labops.RemoveForRestart(os.Stdout, cfg, baseDir, clusterName, flags.keepContext, flags.localOnly, rep); err != nil {
					return fmt.Errorf("could not remove the running lab, nothing was started: %w", err)
				}
			}
			if err := bringUpLab(ctx, cfg, baseDir, flags, rep, start.Session); err != nil {
				if ctx.Err() != nil {
					start.Interrupted()
				}
				return err
			}
			start.Done(clusterName)
			release()
			disarm()
			if flags.browserRun {
				return openBrowserWorkspace(cfg, baseDir, flags)
			}
			return nil
		},
	}

	addParallelFlag(cmd, flags)
	addKeepContextFlag(cmd, flags)
	cmd.Flags().BoolVar(&flags.headless, "test-mode", false, "Deprecated alias for --headless")
	legacy := cmd.Flags().Lookup("test-mode")
	legacy.Hidden, legacy.Deprecated = true, "use --headless instead"
	cmd.Flags().BoolVar(&flags.localOnly, "local-only", false, "Keep this run on this machine: no lab session, progress, results or CPU/memory samples are sent to Astrona, even when signed in")
	cmd.Flags().BoolVar(&withoutLogin, "without-login", false, "Confirm running a catalog lab without signing in or tracking progress on Astrona")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "If the lab is already running, destroy it and start over without asking")
	cmd.Flags().BoolVar(&browser, "browser", false, "Open the lab in the browser workspace once it is ready, without asking")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open the browser workspace or ask about it")
	cmd.MarkFlagsMutuallyExclusive("browser", "no-browser")
	// `astrona connect` runs `run <lab> --session <id>` for "Start on my machine".
	cmd.Flags().StringVar(&flags.adoptSession, "session", "", "Run the lab session the website started for this device (used by astrona connect)")
	_ = cmd.Flags().MarkHidden("session")
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
				fmt.Printf(" Your previous context %s comes back with: astrona destroy %s\n", labops.PreviousContextLabel(sw), clusterName)
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
// none). Shared by run and reset. Cancelling ctx (Ctrl+C) removes what
// was built and returns labops.ErrInterrupted.
func bringUpLab(ctx context.Context, cfg *config.LabConfig, baseDir string, flags *rootFlags, rep *ui.Reporter, sess *labstate.Session) error {
	res, err := labops.Provision(labops.ProvisionRequest{
		Context:        ctx,
		Config:         cfg,
		BaseDir:        baseDir,
		Source:         rememberedSource(flags),
		LocalOnly:      flags.localOnly,
		KeepContext:    flags.keepContext,
		Parallel:       flags.parallel,
		Session:        sess,
		ConfigChecks:   authoring.CheckConfig,
		Warn:           os.Stderr,
		DeferExamClock: flags.browserRun,
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
	} else if cfg.Exam.Enabled() && flags.browserRun {
		fmt.Printf("\nExam time limit: %s. The timer starts when you click Start in the browser.\n", exam.Round(cfg.Exam.Limit()))
	}
	printResources(os.Stdout, res.ResourcesDir, res.Resources)
	if cfg.Metadata.Docs.Question != "" {
		fmt.Printf("\nYour task: astrona docs question%s   (all docs: astrona docs)\n", configFlagHint(baseDir))
	}
	fmt.Printf("Full log: %s\n", res.LogPath)
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

// detachRunLocally forgets the old run's account session before it is
// replaced by a --local-only run, without calling Astrona. The upload block
// is saved first, so a failed teardown or a later command cannot send its
// results.
func detachRunLocally(name string) error {
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	if st == nil {
		st = &labstate.State{}
	}
	oldSession := st.Session
	st.Session, st.LocalOnly = nil, true
	st.StartedAt = ""
	if err := labstate.Save(name, st); err != nil {
		return err
	}
	if oldSession.IsPlayground() {
		labops.StopWatchdog(oldSession.WatchdogPID, name)
	}
	return nil
}
