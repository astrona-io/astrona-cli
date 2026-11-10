package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// destroyOptions are destroy's writers and --keep-resources choice, read
// when the destroy runs: outcomes on stdout, forget warnings on stderr.
func destroyOptions() labops.DestroyOptions {
	return labops.DestroyOptions{
		Out:        os.Stdout,
		ForgetWarn: os.Stderr,
		Forget:     labops.ForgetOptions{KeepResources: keepResources, EndSession: endRunSession},
	}
}

// teardownTrusted applies run's trust check before destroy runs a lab's
// teardown scripts. Unlike run it never fails: destroy is best-effort, so an
// unapproved remote lab (refused, or no terminal and no --trust) is still
// destroyed — only its scripts are skipped. Labs with no teardown scripts
// aren't asked about at all; local labs are trusted as everywhere else.
func teardownTrusted(flags *rootFlags, info labops.Teardown, baseDir string, rep *ui.Reporter) bool {
	if info.Config == nil || !labops.HasTeardownScripts(info) {
		return true
	}
	err := requireTrust(flags, info.Config, baseDir)
	switch {
	case err == nil:
		return true
	case errors.Is(err, labops.ErrNotTrusted):
		rep.Warn("not trusted — skipping the lab's teardown scripts and destroying it without them (pass --trust to run them)")
	default:
		rep.Warn("skipping the lab's teardown scripts: %s — destroying it without them", err) // no terminal: err names --trust
	}
	return false
}

func destroyDiscoveryAllowed() error {
	// Setup ignores selection read errors. Discovery must not turn a broken
	// saved selection into permission to remove an unrelated running lab.
	saved, err := loadCurrentLab()
	if err != nil {
		return fmt.Errorf("read saved lab selection: %w; nothing was destroyed", err)
	}
	if saved != nil {
		return errors.New("a saved lab selection exists but its config could not be loaded; nothing was destroyed")
	}
	return nil
}

// newDestroyCmd builds `astrona destroy`: run teardown scripts, then tear
// down the lab environment (unless the config says keepCluster: true).
// Also best-effort tears down the "test-<lab>" environment `astrona test`
// uses (see cmd_devtest.go) — a cancelled `astrona test` skips Go's defer
// cleanup entirely (Ctrl-C kills the process before it runs), so without
// this a crashed test run would need its own separate cleanup command. One
// `astrona destroy` is enough to clean up whichever of the two you actually
// have running. flags is bound to the root command's persistent flags.
// The destroy itself is labops (DestroyByName, DestroyByPattern,
// DestroyByDiscovery, DestroyLab).
func newDestroyCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "destroy [lab-name|pattern]",
		ValidArgsFunction: labCompletion(nil),
		Short:             "Remove a lab and everything in it (and any leftover `astrona test` copy)",
		Long: "Tear down a lab environment (both the normal run and any leftover 'astrona test' run).\n\n" +
			"With no lab-name, resolves the lab config the same way `run`/`submit` do (-c/--file/--git/--git-ref) " +
			"and falls back to auto-discovering running labs only when no source was selected. " +
			"An unreadable explicit config, catalog lab or saved selection fails without destroying another lab.\n\n" +
			"With a lab-name (as shown by `astrona list`, with or without the '" + apphome.ClusterPrefix() + "' prefix — e.g. '" + apphome.ClusterPrefix() + "my-lab' " +
			"or just 'my-lab'), destroys that lab directly — no config needed, so -c/--file/--git/--git-ref are " +
			"ignored and any teardown scripts are skipped.\n\n" +
			"With a catalog name (astrona.io/ATS016/section-020/module-01/lab-01, or without astrona.io/ as `astrona labs` lists it), destroys that " +
			"lab through its config, teardown scripts included.\n\n" +
			"With a glob pattern (contains *, ?, or [), matches against all currently-running lab names " +
			"(same list `astrona list` shows, with or without the '" + apphome.ClusterPrefix() + "' prefix) and destroys every match — " +
			"e.g. `astrona destroy 'qemu-jumphost-*'` (quote it so your shell doesn't expand the glob itself). " +
			"No config needed, same trade-offs as a single name.\n\n" +
			"A lab's linked clusters (runtime.kind.clusters) are destroyed with it.\n\n" +
			"If `astrona run` pointed kubectl at the lab, the kubectl context you had before comes " +
			"back — only when the lab's context is still current (if you've switched elsewhere, " +
			"that's left as it is). What astrona remembered about the lab (its config, its lab " +
			"session) is forgotten.\n\n" +
			"A remote lab (--git, URL, catalog) that has teardown scripts asks for approval first, like `run` — " +
			"--trust approves it up front. If it isn't approved (declined, or no terminal and no --trust), " +
			"its teardown scripts are skipped and the lab is still destroyed.\n\n" +
			"The lab's copy of its resources (~/" + apphome.Name() + "/resources/<lab>) is removed too; " +
			"--keep-resources keeps it.\n\n" +
			"Port forwards of the lab are always stopped, even when its cluster or VM is already gone.\n\n" +
			"Ctrl+C (or SIGTERM) stops only the step that is running: astrona carries on with the " +
			"remaining steps, so less is left behind. A second Ctrl+C quits at once.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := len(args) > 0 || flags.fromCurrent || flags.gitURL != "" || flags.labArg != ""
			for _, name := range []string{"config", "file", "git", "git-ref"} {
				selected = selected || cmd.Flags().Changed(name)
			}
			labName := "-"
			if len(args) == 1 {
				labName = args[0]
			}
			rep, err := ui.New("destroy", labName, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			// A catalog name (ATS016/…/lab-01) destroys that lab through its
			// config, exactly like `astrona destroy` with -c/--git.
			if len(args) == 1 {
				name, err := catalogLabArg(args[0], flags)
				if err != nil {
					return err
				}
				if name == "" {
					args = nil
				}
			}

			// A destroy half done leaves more behind than one finished:
			// the first Ctrl+C lets it finish, a second one quits. Armed
			// once nothing more is asked (teardownTrusted may ask below).
			finishOnInterrupt := func() func() {
				_, disarm := interruptible(cmd, interruptDestroyNotice)
				return disarm
			}

			if len(args) == 1 {
				defer finishOnInterrupt()()
				if strings.ContainsAny(args[0], "*?[") {
					return labops.DestroyByPattern(args[0], rep, destroyOptions())
				}
				return labops.DestroyByName(args[0], rep, destroyOptions())
			}

			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				if selected {
					return fmt.Errorf("resolve selected lab: %w; nothing was destroyed", err)
				}
				if err := destroyDiscoveryAllowed(); err != nil {
					return err
				}
				rep.Warn("path resolution failed (%s) — falling back to auto-discovery of running astrona labs", err)
				defer finishOnInterrupt()()
				return labops.DestroyByDiscovery(rep, destroyOptions())
			}

			rep.Info("Loading configuration from: %s", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup, loadErr := labops.LoadTeardown(finalPath)
			defer configCleanup()

			if loadErr != nil {
				if selected {
					return fmt.Errorf("load selected lab: %w; nothing was destroyed", loadErr)
				}
				if err := destroyDiscoveryAllowed(); err != nil {
					return err
				}
				rep.Warn("could not load lab config from '%s' (%s) — falling back to auto-discovery of running astrona labs", finalPath, loadErr)
				defer finishOnInterrupt()()
				return labops.DestroyByDiscovery(rep, destroyOptions())
			}

			// A remote lab's teardown scripts are its code too: approve
			// them like run does, but never let a refusal stop the destroy.
			info.SkipScripts = !teardownTrusted(flags, info, baseDir, rep)
			defer finishOnInterrupt()()

			if err := labops.DestroyLab(info, baseDir, rep, destroyOptions()); err != nil {
				return err
			}

			rep.Close()
			fmt.Printf("Lab cluster cleaned up successfully.\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&keepResources, "keep-resources", false, "Keep the lab's resources in ~/"+apphome.Name()+"/resources/<lab> (for reference)")
	return cmd
}
