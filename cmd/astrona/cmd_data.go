package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

func newDataCmd() *cobra.Command {
	return dataCommand(localdata.DefaultStore)
}

func dataCommand(store func() (localdata.Store, error)) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Show local file usage, clean caches, or reset CLI preferences",
		Long: "Show file counts and logical byte sizes under ~/.astrona and the Astrona Git cache. " +
			"Symlink targets, Docker/Podman storage and account-side progress are not included. " +
			"Sparse VM files can occupy less disk space than their logical size. Nothing is sent to Astrona.",
		Args: cobra.NoArgs,
		// Maintenance must work offline and with broken preferences. Do not load
		// the current lab, check releases, or read settings through the root hook.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := store()
			if err != nil {
				return err
			}
			usage, err := s.Usage(cmd.Context())
			if err != nil {
				return err
			}
			if output == "json" {
				return writeJSON(cmd.OutOrStdout(), usage)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "FILES\tSIZE\tLINKS\tPATH")
			var files, size int64
			for _, u := range usage {
				fmt.Fprintf(w, "%d\t%s\t%d\t%q\n", u.Files, formatBytes(u.Bytes), u.Symlinks, u.Path)
				files += u.Files
				size += u.Bytes
			}
			fmt.Fprintf(w, "%d\t%s\t\tTOTAL\n", files, formatBytes(size))
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\nLogical file sizes, not allocated disk space. Symlink targets are excluded.\nPreview cache cleanup: astrona data clean\nPreview preferences reset: astrona data reset\nOld logs: astrona logs clean --older-than 168h --keep 10\nInstalled CLI versions: astrona versions list")
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	cmd.AddCommand(dataChangeCommand(store, false), dataChangeCommand(store, true))
	return cmd
}

func dataChangeCommand(store func() (localdata.Store, error), reset bool) *cobra.Command {
	var yes, dryRun, tempOnly bool
	var output string
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Preview deletion of unused download and repository caches",
		Long: "Preview removal of image, addon and Git caches, plus catalog and update-check metadata. " +
			"Use --yes to delete; --dry-run always previews, even with --yes. " +
			"Saved lab state blocks cleanup, including stopped VMs. Destroy unwanted labs through astrona destroy first. " +
			"Credentials, results, resources, bundles, logs and installed tools are kept. " +
			"Downloads will be needed again, so offline use may be affected. Finish commands running in older CLI versions before cleanup.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			s, err := store()
			if err != nil {
				return err
			}
			apply := yes && !dryRun
			if apply {
				release, err := s.Lock(true)
				if err != nil {
					return err
				}
				defer release()
				if reset {
					releaseSettings, err := s.LockSettings()
					if err != nil {
						return err
					}
					defer releaseSettings()
				}
			}
			var p localdata.Plan
			if reset {
				p, err = s.PlanReset(cmd.Context())
			} else {
				p, err = s.PlanClean(cmd.Context(), tempOnly, time.Now())
			}
			if err != nil {
				return err
			}
			if apply {
				if err := p.Apply(cmd.Context()); err != nil {
					return err
				}
			}
			if output == "json" {
				return writeJSON(cmd.OutOrStdout(), p)
			}
			for _, item := range p.Items {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%q\n", formatBytes(item.Bytes), item.Path)
			}
			for _, note := range p.Skipped {
				fmt.Fprintln(cmd.OutOrStdout(), note)
			}
			if apply {
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %d entries. Deleted files cannot be restored by the CLI.\n", p.Removed)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Preview: %d entries. Nothing changed; use --yes to apply.\n", len(p.Items))
			}
			return nil
		},
	}
	if reset {
		cmd.Use = "reset"
		cmd.Short = "Preview resetting saved preferences and the selected lab"
		cmd.Long = "Remove settings.json, current.json, update-check.json and catalog.json under ~/.astrona. " +
			"Use --yes to apply; --dry-run always previews. This restores preference defaults and forgets the selected lab. " +
			"It does not reset a lab, sign you out, revoke trust, change catalog sources, or delete results or student files. " +
			"For sign-out use astrona logout. Finish commands running in older CLI versions before resetting."
	} else {
		cmd.Flags().BoolVar(&tempOnly, "temp-only", false, "Only remove partial image/addon downloads older than 24h; never scan the system temp directory")
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply deletions; without this flag only show a preview")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without changing files, even with --yes")
	addOutputFlag(cmd, &output)
	return cmd
}
