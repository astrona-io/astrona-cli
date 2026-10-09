package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// newRunRenewCmd is `astrona run renew`: under run, because it keeps a
// playground you started with run going.
func newRunRenewCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "renew [playground]",
		ValidArgsFunction: labCompletion(nil),
		Short:             "Give a running playground its full time limit again",
		Long: "A playground stops and is removed at its time limit (2 hours unless its config.yaml sets " +
			"metadata.timeLimit). Still working in it? `astrona run renew` resets that timer without touching " +
			"the playground: the time so far is sent to your account as playground time — just as " +
			"`astrona destroy` would — and a new timer starts with the full time limit.\n\n" +
			"With no name, every running playground is renewed. A name is the lab as `astrona list` shows it " +
			"(with or without the 'astro-' prefix) or its catalog name:\n\n" +
			"  astrona run renew\n" +
			"  astrona run renew astrona-io/ATS014/section-010/module-01/playground\n\n" +
			"A playground that already stopped can't be renewed — run it again.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			targets, err := runningPlaygrounds(name)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				if name != "" {
					return fmt.Errorf("no running playground named %q — `astrona list` shows what's running", name)
				}
				return errors.New("no playground is running — start one with `astrona run <training>/<section>/<module>/playground`")
			}
			var failed []string
			for _, cluster := range targets {
				if err := renewPlayground(cmd.Context(), cluster); err != nil {
					ui.Warnf("%s: %s", cluster, err)
					failed = append(failed, cluster)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("could not renew %s", strings.Join(failed, ", "))
			}
			return nil
		},
	}
}

// runningPlaygrounds lists the labs whose remembered session is a
// playground: all of them, or the one name matches — its cluster name (with
// or without "astro-"), or its catalog name (with or without the owner).
func runningPlaygrounds(name string) ([]string, error) {
	names, err := labstate.Names()
	if err != nil {
		return nil, err
	}
	want := strings.Trim(name, "/")
	var out []string
	for _, cluster := range names {
		st, err := labstate.Load(cluster)
		if err != nil || st == nil || !st.Session.IsPlayground() {
			continue
		}
		if want == "" || cluster == config.NormalizeClusterName(want) || sameCatalogName(st.Session.Lab, want) {
			out = append(out, cluster)
		}
	}
	return out, nil
}

// sameCatalogName compares catalog names, ignoring a leading owner
// ("astrona-io/ATS014/…" is "ATS014/…").
func sameCatalogName(a, b string) bool {
	trim := func(s string) string {
		s = strings.Trim(s, "/")
		if parts := strings.Split(s, "/"); len(parts) == 5 {
			return strings.Join(parts[1:], "/")
		}
		return s
	}
	return a != "" && strings.EqualFold(trim(a), trim(b))
}

// renewPlayground has the site bank the playground's time so far and start
// a new session with a full time limit, remembers that session and its
// deadline, and replaces the watchdog with one for it.
func renewPlayground(ctx context.Context, cluster string) error {
	st, err := labstate.Load(cluster)
	if err != nil || st == nil || !st.Session.IsPlayground() {
		return errors.New("no playground session is remembered for it")
	}
	client, store, _, err := accountDeps()
	if err != nil {
		return err
	}
	creds, err := client.Active(ctx, store, st.Session.Site)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	noticeOtherSite(creds.Site, "the playground time so far")
	renewed, err := client.RenewLabSession(ctx, store, creds, st.Session.ID)
	if errors.Is(err, account.ErrPlaygroundStopped) {
		return fmt.Errorf("%w — `astrona destroy %s`, then run it again", err, cluster)
	}
	if err != nil {
		return err
	}
	deadline, err := time.Parse(time.RFC3339, renewed.DeadlineAt)
	if err != nil {
		return fmt.Errorf("the site sent no new deadline: %w", err)
	}
	if err := labstate.Update(cluster, func(s *labstate.State) {
		if s.Session != nil && s.Session.ID == st.Session.ID {
			s.Session.ID = renewed.ID
			s.Session.ExpiresAt = renewed.ExpiresAt
			s.Session.DeadlineAt = renewed.DeadlineAt
			s.Session.MaxMinutes = renewed.MaxMinutes
		}
	}); err != nil {
		return fmt.Errorf("renewed on the site, but couldn't save the new deadline: %w", err)
	}
	// A fresh watchdog for the new deadline: the running one may be gone
	// (a reboot) or come from an astrona that doesn't follow renewals.
	stopWatchdog(st.Session.WatchdogPID)
	if pid, err := spawnWatchdog(cluster); err != nil {
		ui.Warnf("couldn't restart the playground's timer (%s) — run `astrona destroy %s` when you are done", err, cluster)
	} else {
		_ = labstate.Update(cluster, func(s *labstate.State) {
			if s.Session != nil {
				s.Session.WatchdogPID = pid
			}
		})
	}
	banked := (time.Duration(renewed.BankedSeconds) * time.Second).Round(time.Minute)
	fmt.Printf("Renewed %s: %s of playground time sent to your account, and a new timer started —\nit now stops and is removed at %s (in %s).\n",
		cluster, banked, deadline.Local().Format("15:04"), time.Until(deadline).Round(time.Minute))
	return nil
}
