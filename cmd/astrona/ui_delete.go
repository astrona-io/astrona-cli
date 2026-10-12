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

func uiDeleteMessage(name string, yes bool) string {
	choices := "[ No, keep running ]    Yes, delete"
	if yes {
		choices = "No, keep running    [ Yes, delete ]"
	}
	return "Delete " + sanitizeLine(name) + " and linked clusters?\n\nAll work will be lost. No saving or submission to Astrona. This cannot be undone.\n\n" + choices + "\nTab/arrows choose · Enter confirms · Esc cancels"
}

func (m uiHomeModel) deleteDialogFits() bool {
	if m.deleting == nil {
		return false
	}
	width := min(76, max(4, m.width-6)) - 4
	return m.width >= 48 && m.height >= 14 && len(strings.Split(ansi.Wrap(uiDeleteMessage(m.deleting.name, m.deleteYes), width, ""), "\n"))+2 <= m.height
}

func forceDeleteUIRun(ctx context.Context, run uiHomeRun, verbose bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.ValidateName(run.name); err != nil {
		return err
	}
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
	// Detach the session before destruction so cleanup cannot upload a result.
	if err := discardUIRun(run.name, &uiRunRestart{name: run.name, runID: run.runID}); err != nil {
		return err
	}
	closeUITerminals(ctx, run.name)
	return destroyByName(run.name, rep)
}
