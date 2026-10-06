package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"astrona/internal/config"
	"astrona/internal/ui"
	"astrona/internal/version"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// dispatchedEnv marks a command handed over to another astrona version, so
// a version that also doesn't fit can't hand it on again.
const dispatchedEnv = "ASTRONA_DISPATCHED_FROM"

// installedVersion is an astrona-<version> binary next to this one.
type installedVersion struct {
	v    version.V
	path string
}

// versionsDir is where `astrona versions install` puts older releases.
func versionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".astrona", "bin"), nil
}

// minHandoverVersion is the oldest astrona a lab is ever handed over to
// (or installed for). astronaVersion comes from the lab, which may be
// someone else's: without this floor a remote lab could ask for a release
// from before trust prompts (added in 0.2.0) and have its scripts run
// without the user ever approving it.
const minHandoverVersion = "0.2.0"

// handoverFloor is minHandoverVersion, parsed.
func handoverFloor() version.V {
	v, err := version.Parse(minHandoverVersion)
	if err != nil {
		panic("minHandoverVersion: " + err.Error())
	}
	return v
}

// installedVersions finds the astrona-<version> binaries in ~/.astrona/bin,
// newest first. Only there — PATH isn't searched, so a stray astrona-*
// binary elsewhere never gets to run a lab.
func installedVersions() []installedVersion {
	dir, err := versionsDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []installedVersion
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), "astrona-")
		if !ok {
			continue
		}
		v, err := version.Parse(name)
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		out = append(out, installedVersion{v, path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].v.Compare(out[j].v) > 0 })
	return out
}

// handoverCandidates are the installed versions a lab may be handed over
// to: those not older than minHandoverVersion.
func handoverCandidates() []installedVersion {
	floor := handoverFloor()
	var out []installedVersion
	for _, iv := range installedVersions() {
		if iv.v.Compare(floor) >= 0 {
			out = append(out, iv)
		}
	}
	return out
}

// belowFloorError explains why a lab that only fits an astrona older than
// minHandoverVersion isn't handed over.
func belowFloorError(c version.Constraint) error {
	return fmt.Errorf("this lab needs astrona %s, and astrona never hands a lab over to a version older than %s — "+
		"those run a lab's scripts without asking you to approve it first\n"+
		"ask the lab's author to allow astrona %s or newer (astronaVersion), or, only if you trust the lab, "+
		"run that version yourself: astrona versions install <version>, then astrona-<version>",
		c, minHandoverVersion, minHandoverVersion)
}

// newestAllowed is the newest of vs that c allows.
func newestAllowed(c version.Constraint, vs []installedVersion) (installedVersion, bool) {
	for _, iv := range vs {
		if c.Allows(iv.v) {
			return iv, true
		}
	}
	return installedVersion{}, false
}

// ensureLabVersion makes sure this astrona may run a lab whose config says
// astronaVersion: c. If not, it hands the whole command over to the newest
// installed astrona-<version> that may — replacing this process, so it
// doesn't return — or explains which version to install.
//
// approve is the lab's trust check. It runs before anything is installed
// or handed over, so this astrona — not the one the lab asks for —
// decides whether a remote lab runs at all.
func ensureLabVersion(constraint string, flags *rootFlags, approve func() error) error {
	if constraint == "" {
		return nil
	}
	c, err := version.ParseConstraint(constraint)
	if err != nil {
		return fmt.Errorf("astronaVersion: %w", err)
	}
	cur, err := version.Parse(Version)
	if err != nil {
		// A developer build has no release number to compare.
		if flags.verbose {
			ui.Infof("this lab wants astrona %s; this is a %s build — not checking", c, Version)
		}
		return nil
	}
	if c.Allows(cur) {
		return nil
	}
	if from := os.Getenv(dispatchedEnv); from != "" {
		return fmt.Errorf("this lab needs astrona %s; astrona %s handed it to %s, which doesn't fit either", c, from, cur)
	}

	target, installed := newestAllowed(c, handoverCandidates())
	var want version.V
	if !installed {
		if want, err = releaseFor(c, cur); err != nil {
			return err
		}
	}
	if err := approve(); err != nil {
		return err
	}
	if !installed {
		if target, err = offerInstall(c, cur, want, flags); err != nil {
			return err
		}
	}
	if target.v.Compare(handoverFloor()) < 0 {
		return belowFloorError(c)
	}

	ui.Infof("this lab needs astrona %s — running it with astrona %s (%s)", c, target.v, target.path)
	argv := append([]string{target.path}, handoverArgs(os.Args[1:], flags)...)
	env := append(os.Environ(), dispatchedEnv+"="+cur.String())
	return execHandover(target.path, argv, env)
}

// execHandover replaces this process with another astrona (a variable so
// tests don't).
var execHandover = syscall.Exec

// promptIn is where the install question's answer is read from.
var promptIn io.Reader = os.Stdin

// releaseFor is the published release a lab needing c is installed with:
// the newest c allows, never one older than minHandoverVersion.
func releaseFor(c version.Constraint, cur version.V) (version.V, error) {
	if want, found := newestRelease(c, handoverFloor()); found {
		return want, nil
	}
	if _, found := newestRelease(c, version.V{}); found {
		return version.V{}, belowFloorError(c)
	}
	return version.V{}, fmt.Errorf("this lab needs astrona %s, and this is %s — no published release fits (`astrona versions available`)", c, cur)
}

// offerInstall installs release want (the newest c allows), when no
// fitting version is installed: without asking with --install-version,
// after a yes at the terminal, and otherwise not at all — a lab config,
// which may come from someone else's repository, never makes astrona
// download and run a program on its own.
func offerInstall(c version.Constraint, cur, want version.V, flags *rootFlags) (installedVersion, error) {
	needs := fmt.Sprintf("this lab needs astrona %s, and this is %s", c, cur)
	if !flags.installVersion {
		if !stdinIsTerminal() {
			return installedVersion{}, fmt.Errorf("%s\ninstall it: astrona versions install %s (or pass --install-version to install it without asking)", needs, want)
		}
		q := fmt.Sprintf("This lab needs astrona %s (this is %s). Download and install astrona %s, verified against GitHub's SHA-256 digest?", c, cur, want)
		if !confirmYes(promptIn, os.Stderr, q) {
			return installedVersion{}, fmt.Errorf("%s\nnot installed — run `astrona versions install %s` when you want it", needs, want)
		}
	}
	path, err := installVersion(want)
	if err != nil {
		return installedVersion{}, err
	}
	return installedVersion{v: want, path: path}, nil
}

// newestRelease is the newest published release c allows that isn't older
// than floor.
func newestRelease(c version.Constraint, floor version.V) (version.V, bool) {
	tags, err := releaseTags()
	if err != nil {
		return version.V{}, false
	}
	var best version.V
	found := false
	for _, t := range tags {
		if v, err := version.Parse(t); err == nil && c.Allows(v) && v.Compare(floor) >= 0 && (!found || v.Compare(best) > 0) {
			best, found = v, true
		}
	}
	return best, found
}

// stdinIsTerminal is a variable so tests can answer for a terminal.
var stdinIsTerminal = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

// installVersion installs release v as ~/.astrona/bin/astrona-<v>
// (verified download) and returns its path. A variable so tests stay
// offline.
var installVersion = func(v version.V) (string, error) {
	dir, err := versionsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "astrona-"+v.String())
	fmt.Fprintf(os.Stderr, "Installing astrona %s (%s), verified against GitHub's SHA-256 digest...\n", v, releaseAssetName())
	if err := downloadVerifiedRelease("v"+v.String(), dest); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Installed %s\n", dest)
	return dest, nil
}

// handoverArgs is the command line for another astrona version: the same
// command and flags, with the lab spelled out as -c/-f/--git/--git-ref — an
// older version may not know `astrona use` or a lab given as an argument.
func handoverArgs(args []string, flags *rootFlags) []string {
	out := make([]string, 0, len(args)+8)
	dropped := false
	for _, a := range args {
		if !dropped && flags.labArg != "" && a == flags.labArg {
			dropped = true
			continue
		}
		// Ours, not the other version's — it would reject an unknown flag.
		if a == "--install-version" || strings.HasPrefix(a, "--install-version=") {
			continue
		}
		out = append(out, a)
	}
	out = append(out, "-c", flags.configPath, "-f", flags.fileName)
	if flags.gitURL != "" {
		out = append(out, "--git", flags.gitURL)
		if flags.gitRef != "" {
			out = append(out, "--git-ref", flags.gitRef)
		}
	}
	return out
}

// labVersionFromLoadError is the astronaVersion of a config this version
// couldn't parse, if it declares one.
func labVersionFromLoadError(err error) string {
	var pe *config.ParseError
	if errors.As(err, &pe) {
		return pe.AstronaVersion
	}
	return ""
}

func newVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Install and list older astrona versions, for labs that need one",
		Long: "A lab can say which astrona releases may run it (astronaVersion in its config, e.g. " +
			"\"<=0.2.1\"). Older releases install side by side as astrona-<version> in " +
			"~/.astrona/bin — the newest stays `astrona` — and `astrona` hands a lab's commands to " +
			"the newest installed version it allows, automatically. If none is installed, it offers " +
			"to install the right one (at a terminal; --install-version installs without asking, " +
			"e.g. in CI). A remote lab must be trusted (--trust) before it's handed over, and never to a " +
			"version older than " + minHandoverVersion + " (the first with trust prompts); only " +
			"~/.astrona/bin is searched, not PATH.\n\n" +
			"Downloads are verified against the SHA-256 digest GitHub records for each release " +
			"binary. Add ~/.astrona/bin to your PATH to run e.g. astrona-0.2.1 directly.",
		RunE: func(cmd *cobra.Command, args []string) error { return listVersions("") },
	}
	var output string
	list := &cobra.Command{
		Use:   "list",
		Short: "List this astrona and the older versions installed next to it",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return listVersions(output) },
	}
	addOutputFlag(list, &output)
	cmd.AddCommand(list)
	cmd.AddCommand(&cobra.Command{
		Use:               "install <version>",
		ValidArgsFunction: versionCompletion(false),
		Short:             "Install an astrona release as astrona-<version> (verified download)",
		Example: `  astrona versions install 0.2.1
  astrona versions install v0.2.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := version.Parse(args[0])
			if err != nil {
				return err
			}
			if _, err := installVersion(v); err != nil {
				return err
			}
			dir, _ := versionsDir()
			fmt.Printf("Labs that need astrona %s now run with it automatically.\n", v)
			if !onPath(dir) {
				fmt.Printf("To run it directly: export PATH=\"%s:$PATH\"\n", dir)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:               "remove <version>",
		ValidArgsFunction: versionCompletion(true),
		Short:             "Remove an installed astrona-<version>",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := version.Parse(args[0])
			if err != nil {
				return err
			}
			dir, err := versionsDir()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, "astrona-"+v.String())
			if err := os.Remove(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("astrona %s isn't installed in %s", v, dir)
				}
				return err
			}
			fmt.Printf("Removed %s\n", path)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "available",
		Short: "List published astrona releases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tags, err := releaseTags()
			if err != nil {
				return err
			}
			for _, t := range tags {
				fmt.Println(strings.TrimPrefix(t, "v"))
			}
			return nil
		},
	})
	return cmd
}

func listVersions(output string) error {
	if err := checkOutput(output); err != nil {
		return err
	}
	if output == "json" {
		type entry struct {
			Version string `json:"version"`
			Path    string `json:"path"`
		}
		out := struct {
			Current   entry   `json:"current"`
			Installed []entry `json:"installed"`
		}{Current: entry{Version: Version, Path: executablePath()}, Installed: []entry{}}
		for _, iv := range installedVersions() {
			out.Installed = append(out.Installed, entry{Version: iv.v.String(), Path: iv.path})
		}
		return printJSON(out)
	}
	fmt.Printf("astrona %s  (%s)\n", Version, executablePath())
	vs := installedVersions()
	if len(vs) == 0 {
		fmt.Println("\nNo older versions installed — `astrona versions install <version>` adds one.")
		return nil
	}
	fmt.Println()
	for _, iv := range vs {
		fmt.Printf("astrona-%-10s %s\n", iv.v, iv.path)
	}
	return nil
}

func executablePath() string {
	p, err := os.Executable()
	if err != nil {
		return "?"
	}
	return p
}

func onPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}
