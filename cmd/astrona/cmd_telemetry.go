package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"astrona/internal/apphome"
	"astrona/internal/settings"
	"astrona/internal/telemetry"
)

// telemetryPurpose opens `astrona telemetry status` and the command's help:
// why the data is collected comes first.
const telemetryPurpose = "The astrona CLI sends usage data only to improve the Astrona platform and its labs:\nwhich labs are used, how long they take, and where people get stuck or hit errors."

// newTelemetryCmd builds `astrona telemetry`: see, turn off, turn on, and
// reset the CLI's usage analytics.
func newTelemetryCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "See or change the usage data the CLI sends (on by default)",
		Long: telemetryPurpose + "\n\n" +
			"It is on by default. `astrona telemetry off` (or ASTRONA_TELEMETRY=0, or DO_NOT_TRACK=1) stops it and deletes the events not yet sent.\n" +
			"Details: " + telemetry.PrivacyURL,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return telemetryStatus(cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether usage data is sent, the install id, and exactly what is collected",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return telemetryStatus(cmd.OutOrStdout())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "off",
		Short: "Stop sending usage data and delete the events not yet sent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := setTelemetry(flags, "false"); err != nil {
				return err
			}
			if err := telemetry.Clear(); err != nil {
				return fmt.Errorf("delete unsent usage events: %w", err)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Usage data is off. Nothing more is sent, and the events not yet sent were deleted.")
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "on",
		Short: "Send usage data again",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := setTelemetry(flags, "true"); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintln(out, "Usage data is on. `astrona telemetry status` shows what is sent."); err != nil {
				return err
			}
			if st := telemetry.Current(); !st.Enabled && st.Reason != "setting" {
				_, err := fmt.Fprintf(out, "Note: %s is set in your environment, so nothing is sent until you unset it.\n", st.Reason)
				return err
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "reset",
		Short: "Replace the random install id with a new one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, err := telemetry.ResetInstallID()
			if err != nil {
				return fmt.Errorf("reset install id: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "New install id: %s\nEvents sent from now on carry it; the old one is no longer used here.\n", id)
			return err
		},
	})
	return cmd
}

func setTelemetry(flags *rootFlags, value string) error {
	path, err := settings.Path(flags.settingsFile)
	if err != nil {
		return err
	}
	return settings.Change(path, "telemetry", &value)
}

func telemetryStatus(out io.Writer) error {
	st := telemetry.Current()
	var b strings.Builder
	b.WriteString(telemetryPurpose + "\n\n")
	switch {
	case st.Enabled:
		b.WriteString("Status: on\n")
	case st.Reason == "setting":
		b.WriteString("Status: off (astrona telemetry off)\n")
	default:
		fmt.Fprintf(&b, "Status: off (%s is set)\n", st.Reason)
	}
	id := telemetry.PeekInstallID()
	if id == "" {
		id = "(none yet: made the first time an event is recorded)"
	}
	fmt.Fprintf(&b, "Install id: %s (random, not derived from this computer; `astrona telemetry reset` replaces it)\n", id)
	fmt.Fprintf(&b, "Events waiting to be sent: %d (in ~/%s/telemetry)\n\n", telemetry.Pending(), apphome.Name())
	b.WriteString(`What is sent, with every batch:
  - the install id above; when you are signed in, your account too
  - this CLI's version, the operating system (linux, darwin, windows) and CPU architecture
What each event says:
  - run started / finished: the catalog lab's name (never a local folder), lab or playground,
    kind cluster or VM, container engine and its major version, how long setup took,
    and ready, cancelled or failed with an error category (for example image_pull or timeout)
  - timer started, submit (attempt number, pass or fail, checks passed of total),
    teardown and how long the session lasted, a lab ended without a submit
  - which workspace tabs were opened or closed (terminal, docs, solution, doc link number)
  - that an update notice was shown
Never sent: your files, file paths, commands, command lines, environment, error messages,
host or user names, or your IP address (the site uses it only to limit request rates).

Turn it off: astrona telemetry off, or set ASTRONA_TELEMETRY=0 or DO_NOT_TRACK=1.
Details: ` + telemetry.PrivacyURL + "\n")
	_, err := io.WriteString(out, b.String())
	return err
}

// showTelemetryNotice prints the one-time notice (stderr, so $(...) captures
// stay clean) the first time a command runs with telemetry on.
func showTelemetryNotice(cmd *cobra.Command) {
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	if path == "telemetry" || strings.HasPrefix(path, "telemetry ") || path == "terms" || path == "version" {
		return
	}
	// Never in a test binary: tests must not write to the real home.
	if testing.Testing() || !telemetry.Current().Enabled || telemetry.NoticeShown() {
		return
	}
	fmt.Fprint(os.Stderr, "\n"+telemetry.Notice+"\n")
	_ = telemetry.MarkNoticeShown()
}
