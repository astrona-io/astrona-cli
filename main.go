package main

import (
	"os"

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

func main() {
	rootCmd := &cobra.Command{
		Use:   "astrona",
		Short: "Astrona is the Astrona lab community CLI",
		Long: "Astrona is the single CLI for the Astrona lab community: spin up local Kubernetes labs, grade them, and (as more groups land) publish and authenticate against the Astrona platform.\n\n" +
			supportLine(),
	}

	// Setting this on rootCmd alone is enough: Cobra falls back to a
	// parent's HelpTemplate for any subcommand that doesn't set its own,
	// so this banner shows on every `--help` screen in the tree.
	rootCmd.SetHelpTemplate(banner() + "\n\n" + helpTemplateBody)

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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
