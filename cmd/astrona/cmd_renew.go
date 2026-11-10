package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
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
			"metadata.timeLimit). Still working in it? `astrona run renew` starts that timer again with the full " +
			"time limit, without touching the playground. Its session goes on, and all of its time counts " +
			"as playground time when it ends.\n\n" +
			"With no name, every running playground is renewed. A name is the lab as `astrona list` shows it " +
			"(with or without the prefix shown in `astrona list`) or its catalog name:\n\n" +
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
// playground, forgetting those whose lab is gone (labops.RunningPlaygrounds).
func runningPlaygrounds(name string) ([]string, error) {
	return labops.RunningPlaygrounds(name, forgetLab)
}

// renewPlayground renews one playground (labops.RenewPlayground) and says
// what it got.
func renewPlayground(ctx context.Context, cluster string) error {
	r, err := labops.RenewPlayground(ctx, os.Stderr, cluster)
	if err != nil {
		return err
	}
	fmt.Printf("Renewed %s: it now stops and is removed at %s (in %s).\n",
		r.Cluster, r.Deadline.Local().Format("15:04"), time.Until(r.Deadline).Round(time.Minute))
	if r.Banked > 0 { // an older site closed the session and started a new one
		fmt.Printf("%s of playground time so far was sent to your account.\n", r.Banked)
	}
	return nil
}
