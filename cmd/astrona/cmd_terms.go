package main

import (
	"fmt"
	"os"
	"strings"

	"astrona/internal/apphome"
	"astrona/internal/terms"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newTermsCmd() *cobra.Command {
	return &cobra.Command{
		Use: "terms", Short: "Read the CLI usage terms and data-loss risk notice", Args: cobra.NoArgs,
		Long:              "Read the exact usage terms shipped with this CLI, without network access.\nPass --accept-terms to record your acceptance in ~/" + apphome.Name() + "/terms.json.",
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\nSHA-256: %s\n", terms.Document(), terms.Digest())
			return err
		},
	}
}

func termsExempt(cmd *cobra.Command) bool {
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	switch path {
	case "help", "docgen", "schema", "__complete", "__completeNoDesc", "playground-watchdog", "port-forward supervise", "stop", "destroy", "logout", "port-forward stop":
		return true
	}
	for _, name := range []string{"completion", "settings", "data"} {
		if path == name || strings.HasPrefix(path, name+" ") {
			return true
		}
	}
	return false
}

func checkTerms(cmd *cobra.Command, explicit bool) error {
	s, err := terms.DefaultStore()
	if err != nil {
		return err
	}
	in, inOK := cmd.InOrStdin().(*os.File)
	out, outOK := cmd.ErrOrStderr().(*os.File)
	interactive := !runTestMode(cmd) && inOK && outOK && isatty.IsTerminal(in.Fd()) && isatty.IsTerminal(out.Fd())
	return s.Ensure(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), interactive, terms.InCI(os.Getenv), explicit, Version)
}

// Child commands can replace Cobra's persistent hook. Wrap each resolved hook
// at the entrypoint so adding a custom hook cannot bypass the terms check.
func installTermsGate(root *cobra.Command, flags *rootFlags, check func(*cobra.Command, bool) error) {
	installCommandGate(root, func(c *cobra.Command) error {
		if !termsExempt(c) && (c.Name() != "terms" || flags.acceptTerms) {
			return check(c, flags.acceptTerms)
		}
		return nil
	})
}

func installCommandGate(root *cobra.Command, check func(*cobra.Command) error) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	var wrap func(*cobra.Command, func(*cobra.Command, []string) error)
	wrap = func(cmd *cobra.Command, inherited func(*cobra.Command, []string) error) {
		before := inherited
		if cmd.PersistentPreRunE != nil {
			before = cmd.PersistentPreRunE
		} else if cmd.PersistentPreRun != nil {
			plain := cmd.PersistentPreRun
			before = func(c *cobra.Command, args []string) error { plain(c, args); return nil }
		}
		cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
			if err := check(c); err != nil {
				return err
			}
			if before != nil {
				return before(c, args)
			}
			return nil
		}
		for _, child := range cmd.Commands() {
			wrap(child, before)
		}
	}
	wrap(root, nil)
}
