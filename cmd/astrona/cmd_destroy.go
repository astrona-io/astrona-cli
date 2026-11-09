package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labops"
	"astrona/internal/ui"

	"github.com/spf13/cobra"
)

// teardownInfo is what `astrona destroy` needs from the lab config
// (labops.Teardown).
type teardownInfo = labops.Teardown

// destroyOptions are destroy's writers and --keep-resources choice, read
// when the destroy runs: outcomes on stdout, forget warnings on stderr.
func destroyOptions() labops.DestroyOptions {
	return labops.DestroyOptions{
		Out:        os.Stdout,
		ForgetWarn: os.Stderr,
		Forget:     labops.ForgetOptions{KeepResources: keepResources, EndSession: endRunSession},
	}
}

// Thin wrappers over the labops destroy, so call sites keep their names.
// S15 removes them.
func destroyByName(name string, rep *ui.Reporter) error {
	return labops.DestroyByName(name, rep, destroyOptions())
}

func destroyByNameLocked(name string, rep *ui.Reporter) error {
	return labops.DestroyByNameLocked(name, rep, destroyOptions())
}

func destroyLab(info teardownInfo, baseDir string, rep *ui.Reporter) error {
	return labops.DestroyLab(info, baseDir, rep, destroyOptions())
}

func tearDownLabEnvironment(clusterName string, info teardownInfo, baseDir string, hardFail bool, rep *ui.Reporter) error {
	return labops.TearDownLabEnvironment(clusterName, info, baseDir, hardFail, rep)
}

// qemuStateExists and kindClusterExists: see hypervisor.StateExists and
// cluster.Exists.
func qemuStateExists(name string) bool { return hypervisor.StateExists(name) }

func kindClusterExists(name string) bool { return cluster.Exists(name) }

// teardownTrusted applies run's trust check before destroy runs a lab's
// teardown scripts. Unlike run it never fails: destroy is best-effort, so an
// unapproved remote lab (refused, or no terminal and no --trust) is still
// destroyed — only its scripts are skipped. Labs with no teardown scripts
// aren't asked about at all; local labs are trusted as everywhere else.
func teardownTrusted(flags *rootFlags, info teardownInfo, baseDir string, rep *ui.Reporter) bool {
	if info.Config == nil || !labops.HasTeardownScripts(info) {
		return true
	}
	err := requireTrust(flags, info.Config, baseDir)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errNotTrusted):
		rep.Warn("not trusted — skipping the lab's teardown scripts and destroying it without them (pass --trust to run them)")
	default:
		rep.Warn("skipping the lab's teardown scripts: %s — destroying it without them", err) // no terminal: err names --trust
	}
	return false
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
			"and falls back to auto-discovering running labs if that fails.\n\n" +
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
			"--keep-resources keeps it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if len(args) == 1 {
				if strings.ContainsAny(args[0], "*?[") {
					return labops.DestroyByPattern(args[0], rep, destroyOptions())
				}
				return destroyByName(args[0], rep)
			}

			finalPath, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, flags.verbose)
			if err != nil {
				rep.Warn("path resolution failed (%s) — falling back to auto-discovery of running astrona labs", err)
				return labops.DestroyByDiscovery(rep, destroyOptions())
			}

			rep.Info("Loading configuration from: %s", finalPath)

			baseDir := filepath.Dir(finalPath)
			info, configCleanup, loadErr := labops.LoadTeardown(finalPath)
			defer configCleanup()

			if loadErr != nil {
				rep.Warn("could not load lab config from '%s' (%s) — falling back to auto-discovery of running astrona labs", finalPath, loadErr)
				return labops.DestroyByDiscovery(rep, destroyOptions())
			}

			// A remote lab's teardown scripts are its code too: approve
			// them like run does, but never let a refusal stop the destroy.
			info.SkipScripts = !teardownTrusted(flags, info, baseDir, rep)

			if err := destroyLab(info, baseDir, rep); err != nil {
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
