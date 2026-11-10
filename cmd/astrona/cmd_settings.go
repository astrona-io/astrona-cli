package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"astrona/internal/apphome"
	"astrona/internal/settings"
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
			"set to save a value, unset to restore one default, reset to restore all of them, and path to locate the file. " +
			"Boolean settings use yes/no; true/false and on/off are also accepted.",
		Example: `  astrona settings
  astrona settings set keep-context yes
  astrona settings set usage-samples off   # send no lab CPU/memory samples
  astrona settings set parallel 2
  astrona settings set cache-retention-days 30
  astrona settings set cache-max-size-gb 50
  astrona settings get keep-context
  astrona settings unset keep-context
  astrona settings reset --yes`,
		Args: cobra.NoArgs,
		// Settings are local maintenance: no current lab, version check, or
		// root settings load. In particular, path works with a damaged file.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output, "text"); err != nil {
				return err
			}
			path, err := settings.Path(flags.settingsFile)
			if err != nil {
				return err
			}
			saved, err := settings.Load(path)
			if err != nil {
				return err
			}
			if output == "json" {
				values := settings.Values{}
				for _, def := range settings.Definitions() {
					values[def.Key], _ = saved.Value(def.Key) // definitions are known keys
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(values)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Settings: %s\n\n", path)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE\tDESCRIPTION")
			for _, def := range settings.Definitions() {
				value, _ := saved.Value(def.Key) // definitions are known keys
				source := "default"
				if _, ok := saved[def.Key]; ok {
					source = "saved"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", def.Key, settings.ValueText(string(value)), source, def.Description)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "Output format: text or json (saved values plus defaults)")
	cmd.AddCommand(newSettingsViewCmd(flags), newSettingsReviewCmd(flags))
	cmd.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the settings file path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settings.Path(flags.settingsFile)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), path)
			return err
		},
	})
	cmd.AddCommand(newSettingsResetCmd(flags))
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
				path, err := settings.Path(flags.settingsFile)
				if err != nil {
					return err
				}
				if action == "get" {
					saved, err := settings.Load(path)
					if err != nil {
						return err
					}
					value, err := saved.Value(args[0])
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(cmd.OutOrStdout(), settings.ValueText(string(value)))
					return err
				}
				var value *string
				if action == "set" {
					value = &args[1]
				}
				if err := settings.Change(path, args[0], value); err != nil {
					return err
				}
				if value == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "%s restored to its default\n", args[0])
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], settings.ValueText(*value))
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
		for _, def := range settings.Definitions() {
			values = append(values, def.Key+"\t"+def.Description)
		}
	} else if len(args) == 1 && cmd.Name() == "set" {
		switch args[0] {
		case "verbose", "keep-context", "update-check", "login-reminder", "usage-samples":
			values = []string{"yes", "no"}
		case "browser":
			values = []string{settings.BrowserAsk, settings.BrowserAlways, settings.BrowserNever}
		}
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}

func applyUserSettings(cmd *cobra.Command, flags *rootFlags) error {
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return err
	}
	saved, err := settings.Load(path)
	if err != nil {
		return err
	}
	flags.settingsArgs = nil
	for _, def := range settings.Definitions() {
		value, ok := saved[def.Key]
		// The browser preference is read by run itself: its --browser flag
		// is a one-run answer, not the saved ask/always/never.
		if def.Key == "browser" {
			continue
		}
		flag := cmd.Flags().Lookup(def.Key)
		if !ok || flag == nil || flag.Changed {
			continue
		}
		if err := flag.Value.Set(string(value)); err != nil {
			return fmt.Errorf("apply setting %s: %w", def.Key, err)
		}
		// Older releases do not read settings.json. Forward lab preferences
		// as flags so version hand-over cannot silently drop them.
		if def.Key != "update-check" && string(value) != def.Default {
			flags.settingsArgs = append(flags.settingsArgs, "--"+def.Key+"="+string(value))
		}
	}
	return nil
}

func newSettingsResetCmd(flags *rootFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Restore every preference to its default",
		Long: "Remove all saved preferences from settings.json so every setting uses its default again. " +
			"Without --yes it only shows what would change. It also repairs a settings file that astrona refuses " +
			"to read. It does not touch your lab, sign-in, trust decisions or catalog sources; for those, and " +
			"for forgetting the selected lab, see `astrona data reset`. Restore a single setting with `astrona settings unset <key>`.",
		Example: `  astrona settings reset           # show what would be reset
  astrona settings reset --yes     # reset every setting to its default`,
		Args: cobra.NoArgs,
		// Like the other settings commands: local maintenance that must work
		// with a damaged file, so no root settings load.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settings.Path(flags.settingsFile)
			if err != nil {
				return err
			}
			saved, damaged, existed, err := settings.Reset(path, yes)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			defaults := map[string]string{}
			for _, def := range settings.Definitions() {
				defaults[def.Key] = def.Default
			}
			switch {
			case !existed:
				fmt.Fprintf(out, "No saved settings in %s: every setting already uses its default.\n", path)
				return nil
			case damaged && yes:
				fmt.Fprintf(out, "Replaced the unreadable settings file %s: every setting uses its default.\n", path)
				return nil
			case damaged:
				fmt.Fprintf(out, "The settings file %s cannot be read as settings. Run `astrona settings reset --yes` to replace it with the defaults.\n", path)
				return nil
			}
			keys := make([]string, 0, len(saved))
			for key := range saved {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			verb := "Would reset"
			if yes {
				verb = "Reset"
			}
			fmt.Fprintf(out, "%s %s in %s:\n\n", verb, pluralSettings(len(keys)), path)
			fmt.Fprintln(w, "SETTING\tSAVED\tDEFAULT")
			for _, key := range keys {
				fmt.Fprintf(w, "%s\t%s\t%s\n", key, settings.ValueText(string(saved[key])), settings.ValueText(defaults[key]))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if !yes {
				fmt.Fprintln(out, "\nNothing changed. Run `astrona settings reset --yes` to apply.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the reset; without this flag only show what would change")
	return cmd
}

func pluralSettings(n int) string {
	if n == 1 {
		return "1 saved setting"
	}
	return fmt.Sprintf("%d saved settings", n)
}
