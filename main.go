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
	// namespace under. --config/-c and --file/-f are persistent flags here
	// so every subcommand shares them without repeating the declaration.
	var configPath string
	var fileName string
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", ".", "Path or URL to the lab configuration YAML file")
	rootCmd.PersistentFlags().StringVarP(&fileName, "file", "f", "config.yaml", "Configuration file name override")

	rootCmd.AddCommand(newRunCmd(&configPath, &fileName))
	rootCmd.AddCommand(newDestroyCmd(&configPath, &fileName))
	rootCmd.AddCommand(newSubmitCmd(&configPath, &fileName))
	rootCmd.AddCommand(newTestCmd(&configPath, &fileName))

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
