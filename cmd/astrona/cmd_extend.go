package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/labops"
)

// newRunExtendCmd is `astrona run extend`: under run, beside renew, because
// it gives a lab or playground you started with run more time.
func newRunExtendCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "extend [lab]",
		ValidArgsFunction: labCompletion(nil),
		Short:             "Add " + strconv.Itoa(labops.ExtendMinutes) + " minutes to a running lab or playground",
		Long: "Need more time? `astrona run extend` adds " + strconv.Itoa(labops.ExtendMinutes) + " minutes to a running lab or " +
			"playground, each time you run it — there is no limit on how often. It is the same as \"Need more time\" on the " +
			"lab page and in the browser workspace. A playground then stops and is removed that much later; a lab gets the " +
			"time on its expected time (and on its time limit when it needs it). The run is marked as extended on your account " +
			"and keeps its score.\n\n" +
			"Only a run with an Astrona lab session can be extended: one you started signed in, from the catalog. A " +
			"--local-only run, a signed-out run and a lab from your own files have no Astrona time limit to move. A hands-on " +
			"exam's time is never extended, and neither is a lab whose clock hasn't started yet or that already ended.\n\n" +
			"With no name, the only running lab is extended. A name is the lab as `astrona list` shows it (with or without " +
			"the prefix shown in `astrona list`) or its catalog name:\n\n" +
			"  astrona run extend\n" +
			"  astrona run extend astrona-io/ATS014/section-010/module-01/playground\n\n" +
			"`astrona run renew` instead starts a playground's full time limit over.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = strings.Trim(args[0], "/")
			}
			targets, err := labops.RunningLabs(name)
			if err != nil {
				return err
			}
			switch {
			case len(targets) == 0 && name != "":
				return fmt.Errorf("no running lab named %q — `astrona list` shows what's running", name)
			case len(targets) == 0:
				return errors.New("no lab is running — nothing to extend")
			case len(targets) > 1:
				return fmt.Errorf("more than one lab is running (%s) — say which: `astrona run extend <lab>`", strings.Join(targets, ", "))
			}
			r, err := labops.ExtendRun(cmd.Context(), os.Stderr, targets[0], nil)
			if err != nil {
				if labops.IsExtendRefused(err, "conflict") {
					return fmt.Errorf("%s: %w — run `astrona run extend` again", targets[0], err)
				}
				return fmt.Errorf("%s: %w", targets[0], err)
			}
			if r.Playground {
				fmt.Printf("Added %d minutes to %s: it now stops and is removed at %s (in %s).\n",
					labops.ExtendMinutes, r.Cluster, r.Deadline.Local().Format("15:04"), time.Until(r.Deadline).Round(time.Minute))
			} else {
				fmt.Printf("Added %d minutes to %s on your account.\n", labops.ExtendMinutes, r.Cluster)
			}
			if r.ExtraMinutes > 0 {
				fmt.Printf("%d minutes added to this run so far.\n", r.ExtraMinutes)
			}
			return nil
		},
	}
}
