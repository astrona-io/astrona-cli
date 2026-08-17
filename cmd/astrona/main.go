package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
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
}

// Version is the current version of the astrona-cli binary, burnt in at build
// time via -ldflags "-X main.Version=vX.Y.Z".
var Version = "developer"

func checkLatestVersion() {
	client := &http.Client{
		Timeout: 800 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Head("https://github.com/astrona-io/astrona-cli/releases/latest")
	if err != nil {
		fmt.Println("[WARN] check version not possible")
		return
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if location == "" {
		fmt.Println("[WARN] check version not possible")
		return
	}

	parts := strings.Split(strings.TrimRight(location, "/"), "/")
	if len(parts) == 0 {
		fmt.Println("[WARN] check version not possible")
		return
	}
	latestTag := parts[len(parts)-1]

	if latestTag != "" && latestTag != Version {
		fmt.Printf("[INFO] A new version of astrona is available: %s (current: %s). Please upgrade!\n\n", latestTag, Version)
	}
}

func main() {
	if askpassPass := os.Getenv("ASTRONA_INTERNAL_ASKPASS"); askpassPass != "" {
		fmt.Println(askpassPass)
		os.Exit(0)
	}

	rootCmd := &cobra.Command{
		Use:     "astrona",
		Short:   "Astrona is the Astrona lab community CLI",
		Long:    "Astrona is the single CLI for the Astrona lab community: spin up local Kubernetes labs, grade them, and (as more groups land) publish and authenticate against the Astrona platform.\n\n" + supportLine(),
		Version: Version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if cmd.Name() == "__complete" || cmd.Name() == "help" {
				return
			}
			checkLatestVersion()
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
	flags := &rootFlags{}
	rootCmd.PersistentFlags().StringVarP(&flags.configPath, "config", "c", ".", "Path or URL to the lab config directory (with --git: a subdirectory within the cloned repo)")
	rootCmd.PersistentFlags().StringVarP(&flags.fileName, "file", "f", "config.yaml", "Configuration file name override")
	rootCmd.PersistentFlags().StringVar(&flags.gitURL, "git", "", "Git repository URL to clone/pull (https://, git@host:, or ssh://) — --config then selects a subdirectory within it")
	rootCmd.PersistentFlags().StringVar(&flags.gitRef, "git-ref", "", "Git branch, tag, or commit to check out (used with --git; default: the repo's default branch)")

	rootCmd.AddCommand(newRunCmd(flags))
	rootCmd.AddCommand(newDestroyCmd(flags))
	rootCmd.AddCommand(newSubmitCmd(flags))
	rootCmd.AddCommand(newTestCmd(flags))
	rootCmd.AddCommand(newCheckCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newImagesCmd())
	rootCmd.AddCommand(newSSHCmd())
	rootCmd.AddCommand(newUpgradeCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
