package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"astrona/internal/account"
	"astrona/internal/labops"

	"github.com/spf13/cobra"
)

func newDevicesCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List the computers signed in to your Astrona account",
		Long: "List every computer signed in to your Astrona account with astrona, this one marked *. " +
			"ONLINE means it runs `astrona connect`, so the website can start labs on it.\n\n" +
			"Sign a computer out, rename it or pick the one lab pages preselect under Settings → Devices " +
			"on the website.\n\n" +
			"With -o json, stdout is the list as the site returns it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			client, store, site, err := labops.AccountDeps()
			if err != nil {
				return err
			}
			ctx := context.Background()
			creds, err := client.Active(ctx, store, site)
			if errors.Is(err, account.ErrSignedOut) {
				return fmt.Errorf("you're not signed in to Astrona (%s) — sign in with: astrona login", site)
			}
			if err != nil {
				return err
			}
			devices, err := client.ListDevices(ctx, store, creds)
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(devices)
			}
			printDevices(devices, time.Now())
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

func printDevices(devices []account.DeviceInfo, now time.Time) {
	if len(devices) == 0 {
		fmt.Println("No computers are signed in.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  NAME\tSYSTEM\tENGINE\tVERSION\tCONNECTED")
	for _, d := range devices {
		mark := " "
		if d.Current {
			mark = "*"
		}
		name := d.Name
		if d.IsDefault {
			name += " (default)"
		}
		system := d.OS
		if d.Arch != "" {
			system += "/" + d.Arch
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\t%s\n", mark, name, system, dash(d.Engine), dash(d.CLIVersion), connectedState(d, now))
	}
	_ = w.Flush()
}

func connectedState(d account.DeviceInfo, now time.Time) string {
	switch {
	case d.Online:
		return "online"
	case d.LastSeenAt == nil:
		return "never"
	default:
		return "last " + roughAgo(now.Sub(*d.LastSeenAt)) + " ago"
	}
}

func roughAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
