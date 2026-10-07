package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/ui"
	"astrona/internal/version"
)

// helpTemplateBody is Cobra's own default help template (see
// cobra.Command.HelpTemplate), reused as-is below the banner so overriding
// it doesn't change how Long/Short/usage actually render.
const helpTemplateBody = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

// rootFlags holds every persistent flag shared across subcommands. Each
// newXCmd constructor takes one *rootFlags instead of a growing list of
// *string/*bool params — the values are filled in by Cobra's flag parsing
// after newXCmd is called but before RunE runs, so they're read through
// this shared pointer rather than passed by value.
type rootFlags struct {
	configPath string
	fileName   string
	gitURL     string
	gitRef     string
	verbose    bool
	trust      bool
	// labArg is the lab given as a command's argument (useLabArg), so a
	// hand-over to another astrona version can spell it as -c instead.
	labArg string
	// fromCurrent: the lab came from `astrona use` (applyCurrentLab).
	fromCurrent bool
	// catalogLab is the catalog name (ATS014/section-010/module-01/lab-02)
	// when the lab came from the catalog — given as the argument or picked
	// with `astrona use`. Only catalog labs are tied to an Astrona account.
	catalogLab string
	// installVersion: --install-version, install the astrona release a lab
	// requires without asking.
	installVersion bool
	// gitExplicit: --git was given on this command line (so a lab
	// argument is a subdirectory of that repo, not a local path).
	gitExplicit bool
	// parallel is --parallel on commands that create a lab (run, reset,
	// test): linked clusters created at once.
	parallel int
}

// Version is the current version of the astrona-cli binary, burnt in at build
// time via -ldflags "-X main.Version=vX.Y.Z".
var Version = "developer"

// newerRelease reports whether latest is a newer release than current — by
// version, not by tag text (v0.2.1 isn't "new" for v0.2.2). A developer
// build has no version to compare and isn't nagged.
func newerRelease(latest, current string) bool {
	l, err := version.Parse(latest)
	if err != nil {
		return false
	}
	c, err := version.Parse(current)
	if err != nil {
		return false
	}
	return l.Compare(c) > 0
}

func checkLatestVersion(verbose bool) {
	if !updateCheckWanted() {
		return
	}
	path, err := updateStatePath()
	if err != nil {
		return
	}
	st := loadUpdateState(path)
	now := time.Now()
	if st.dueForCheck(now) {
		tag, err := fetchLatestTag()
		st.CheckedAt = now
		st.Failed = err != nil
		if err == nil {
			st.Latest = tag
		} else if verbose {
			ui.Warnf("could not check for a new astrona version: %s", err)
		}
		saveUpdateState(path, st)
	}
	if st.dueForNotice(Version, now) {
		// stderr, not stdout: commands like `astrona kubeconfig` are meant
		// for $(...) capture, and the notice must not end up in it.
		ui.Infof("astrona %s is available (you have %s) — `astrona upgrade` · what's new: https://github.com/%s/releases/tag/%s\n",
			st.Latest, Version, releaseRepo, st.Latest)
		st.NotifiedAt = now
		saveUpdateState(path, st)
	}
}

// fetchLatestTag asks GitHub for the latest release tag (the redirect of
// /releases/latest — no API rate limit).
func fetchLatestTag() (string, error) {
	client := &http.Client{
		Timeout: 800 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head("https://github.com/" + releaseRepo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	location := resp.Header.Get("Location")
	tag := location[strings.LastIndex(strings.TrimRight(location, "/"), "/")+1:]
	if location == "" || tag == "" {
		return "", fmt.Errorf("no latest release redirect")
	}
	return tag, nil
}

// newRootCmd builds the full astrona command tree. It's factored out of
// main() so cmd/docgen can construct the identical tree (same Use/Short/Long
// and flags Cobra's own doc generator reads) without duplicating it.
func newRootCmd(flags *rootFlags) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "astrona",
		Short:   "Astrona is the Astrona lab community CLI",
		Long:    "Astrona is the single CLI for the Astrona lab community: spin up local Kubernetes labs, grade them, and (as more groups land) publish and authenticate against the Astrona platform.\n\n" + supportLine(),
		Version: Version,
		// main prints errors itself, in color (ui.PrintError).
		SilenceErrors: true,
		// Plain `astrona`: which lab, its state, what to do next.
		Run: homeRun(flags),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// The lab picked with `astrona use`, unless this command
			// names one or runs inside a lab directory.
			flags.gitExplicit = cmd.Flags().Changed("git")
			if cmd.Name() != "use" {
				applyCurrentLab(cmd, flags)
			}
			// Hidden commands (docgen, the port-forward supervisor) are
			// tooling/background processes — no version nag, no network.
			if cmd.Name() == "__complete" || cmd.Name() == "help" || cmd.Hidden {
				return
			}
			checkLatestVersion(flags.verbose)
		},
	}

	// Setting this on rootCmd alone is enough: Cobra falls back to a
	// parent's HelpTemplate for any subcommand that doesn't set its own,
	// so this banner shows on every `--help` screen in the tree.
	rootCmd.SetHelpTemplate(banner() + "\nVersion: " + Version + "\n\n" + helpTemplateBody)

	// A RunE error (a real runtime failure — bad config, a broken image, a
	// failed exec.Command) has nothing to do with how the command was
	// invoked, so dumping the full flags/usage block after it is just noise
	// for the operator trying to read the actual error. SilenceUsage on
	// rootCmd is inherited by every subcommand (Cobra only needs it false on
	// either the leaf or root to print), so this is the one place to set it.
	// A genuine flag mistake (unknown flag, bad value) still shows usage —
	// that error path is routed through FlagErrorFunc instead, which prints
	// it manually before returning.
	rootCmd.SilenceUsage = true
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.Println(cmd.UsageString())
		return err
	})

	// Flat, podman-run-style verbs at the root — no `lab`/`dev` noun to
	// namespace under. --config/-c, --file/-f, --git, and --git-ref are
	// persistent flags here so every subcommand shares them without
	// repeating the declaration.
	//
	// With --git set, --config switches meaning: instead of a local path or
	// a direct config-file URL, it's the subdirectory *within* the cloned
	// repo to use (default "." — the repo root). --git itself takes any URL
	// `git clone` accepts (https://, git@host:, ssh://) — the transport,
	// including SSH auth via your own agent/keys, is entirely git's own.
	rootCmd.PersistentFlags().StringVarP(&flags.configPath, "config", "c", ".", "Path or URL to the lab config directory (with --git: a subdirectory within the cloned repo)")
	rootCmd.PersistentFlags().StringVarP(&flags.fileName, "file", "f", "config.yaml", "Configuration file name override")
	rootCmd.PersistentFlags().StringVar(&flags.gitURL, "git", "", "Git repository URL to clone/pull (https://, git@host:, or ssh://) — --config then selects a subdirectory within it")
	rootCmd.PersistentFlags().StringVar(&flags.gitRef, "git-ref", "", "Git branch, tag, or commit to check out (used with --git; default: the repo's default branch)")
	// No -v shorthand: Cobra reserves it for the auto-generated --version flag.
	rootCmd.PersistentFlags().BoolVar(&flags.installVersion, "install-version", false, "If the lab needs another astrona release (astronaVersion) that isn't installed, install it without asking — for CI")
	rootCmd.PersistentFlags().BoolVar(&flags.trust, "trust", false, "Approve running a remote (--git / URL) lab without asking — for CI; review the lab first")
	rootCmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "Stream the full output of every underlying command instead of the compact step view")

	// Commands are grouped by who uses them and listed in the order you'd
	// use them (see commandGroups), not alphabetically.
	for _, g := range commandGroups(flags) {
		rootCmd.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
		for _, c := range g.cmds {
			c.GroupID = g.id
			rootCmd.AddCommand(c)
		}
	}
	// Ungrouped: listed under "Additional Commands" with completion and help.
	rootCmd.AddCommand(newDocgenCmd(flags))
	rootCmd.AddCommand(newSchemaCmd())

	return rootCmd
}

func main() {
	if askpassPass := os.Getenv("ASTRONA_INTERNAL_ASKPASS"); askpassPass != "" {
		fmt.Println(askpassPass)
		os.Exit(0)
	}

	rootCmd := newRootCmd(&rootFlags{})

	if err := rootCmd.Execute(); err != nil {
		ui.PrintError(err)
		os.Exit(exitCodeFor(err))
	}
}
