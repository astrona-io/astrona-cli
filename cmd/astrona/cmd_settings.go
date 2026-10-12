package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"astrona/internal/apphome"
)

func newSettingsCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "View and change your saved CLI preferences",
		Long: "View saved preferences and defaults from ~/" + apphome.Name() + "/settings.json. " +
			"Explicit command-line flags override saved preferences. Settings never store " +
			"credentials or approve remote code. Viewing settings does not create a file.\n\n" +
			"Use view for a detailed overview, review to approve settings changes, " +
			"set to save a value, unset to restore its default, and path to locate the file.",
		Example: `  astrona settings
  astrona settings set keep-context true
  astrona settings set parallel 2
  astrona settings set cache-retention-days 30
  astrona settings set cache-max-size-gb 50
  astrona settings get keep-context
  astrona settings unset keep-context`,
		Args: cobra.NoArgs,
		// Settings are local maintenance: no current lab, version check, or
		// root settings load. In particular, path works with a damaged file.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output, "text"); err != nil {
				return err
			}
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			saved, err := loadSettings(path)
			if err != nil {
				return err
			}
			if output == "json" {
				values := userSettings{}
				for _, def := range settingsDefinitions() {
					values[def.key], _ = saved.value(def.key) // definitions are known keys
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(values)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Settings: %s\n\n", path)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE\tDESCRIPTION")
			for _, def := range settingsDefinitions() {
				value, _ := saved.value(def.key) // definitions are known keys
				source := "default"
				if _, ok := saved[def.key]; ok {
					source = "saved"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", def.key, value, source, def.description)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "Output format: text or json (saved values plus defaults)")
	cmd.AddCommand(newSettingsViewCmd(flags), newSettingsReviewCmd(flags))
	cmd.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the settings file path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), path)
			return err
		},
	})
	for _, action := range []string{"get", "set", "unset"} {
		use, short, nargs := action+" <key>", "Read a saved value or its default", 1
		switch action {
		case "set":
			use, short, nargs = "set <key> <value>", "Save a preference for future commands", 2
		case "unset":
			short = "Remove a saved preference and restore its default"
		}
		cmd.AddCommand(&cobra.Command{
			Use: use, Short: short, Args: cobra.ExactArgs(nargs),
			ValidArgsFunction: completeSetting,
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := settingsPath(flags)
				if err != nil {
					return err
				}
				if action == "get" {
					saved, err := loadSettings(path)
					if err != nil {
						return err
					}
					value, err := saved.value(args[0])
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(cmd.OutOrStdout(), string(value))
					return err
				}
				var value *string
				if action == "set" {
					value = &args[1]
				}
				if err := changeSetting(path, args[0], value); err != nil {
					return err
				}
				if value == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "%s restored to its default\n", args[0])
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], *value)
				}
				return nil
			},
		})
	}
	return cmd
}

func completeSetting(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var values []string
	if len(args) == 0 {
		for _, def := range settingsDefinitions() {
			values = append(values, def.key+"\t"+def.description)
		}
	} else if len(args) == 1 && cmd.Name() == "set" {
		switch args[0] {
		case "verbose", "keep-context", "update-check":
			values = []string{"true", "false"}
		}
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}

func applyUserSettings(cmd *cobra.Command, flags *rootFlags) error {
	path, err := settingsPath(flags)
	if err != nil {
		return err
	}
	saved, err := loadSettings(path)
	if err != nil {
		return err
	}
	flags.settingsArgs = nil
	for _, def := range settingsDefinitions() {
		value, ok := saved[def.key]
		flag := cmd.Flags().Lookup(def.key)
		if !ok || flag == nil || flag.Changed {
			continue
		}
		if err := flag.Value.Set(string(value)); err != nil {
			return fmt.Errorf("apply setting %s: %w", def.key, err)
		}
		// Older releases do not read settings.json. Forward lab preferences
		// as flags so version hand-over cannot silently drop them.
		if def.key != "update-check" && string(value) != def.defaultValue {
			flags.settingsArgs = append(flags.settingsArgs, "--"+def.key+"="+string(value))
		}
	}
	return nil
}
