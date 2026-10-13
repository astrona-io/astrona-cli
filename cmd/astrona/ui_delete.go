package main

import (
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func uiDeleteMessage(run uiHomeRun, yes bool) string {
	choices := "[ No, keep running ]    Yes, delete"
	if yes {
		choices = "No, keep running    [ Yes, delete ]"
	}
	tracking := "Unsubmitted lab work will be lost. No results will be submitted to Astrona."
	if run.reportPlayground {
		tracking = "This playground's session and resource usage will be reported to Astrona. No evaluation is needed."
	} else if strings.HasPrefix(run.kind, "Playground") {
		tracking = "This playground is not tracked. No data will be sent to Astrona."
	}
	return "Delete " + sanitizeLine(run.name) + " and all its VMs and linked clusters?\n\nAll work will be lost. This cannot be undone.\n" + tracking + "\n\n" + choices + "\nTab/arrows choose · Enter confirms · Esc cancels"
}

func (m uiHomeModel) deleteDialogFits() bool {
	if m.deleting == nil {
		return false
	}
	width := min(76, max(4, m.width-6)) - 4
	return m.width >= 48 && m.height >= 14 && len(strings.Split(ansi.Wrap(uiDeleteMessage(*m.deleting, m.deleteYes), width, ""), "\n"))+2 <= m.height
}

func forceDeleteUIRun(ctx context.Context, run uiHomeRun, verbose bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.ValidateName(run.name); err != nil {
		return err
	}
	release, err := labstate.LockMutation(run.name)
	if err != nil {
		return err
	}
	defer release()
	st, err := labstate.Load(run.name)
	if err != nil {
		return err
	}
	if st == nil || workspaceRunID(st) != run.runID {
		return fmt.Errorf("the selected run changed; nothing was deleted. Select it again")
	}
	rep, err := ui.NewReporter("destroy", run.name, verbose)
	if err != nil {
		return err
	}
	defer rep.Close()
	uiPaneTitle("DESTROY / " + run.name)
	// Playgrounds report on deletion; labs require an explicit submission.
	if !st.Session.IsPlayground() {
		if err := discardUIRun(run.name, &uiRunRestart{name: run.name, runID: run.runID}); err != nil {
			return err
		}
	}
	closeUITerminals(ctx, run.name)
	return destroyByNameLocked(run.name, rep)
}
