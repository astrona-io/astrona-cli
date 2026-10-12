package main

import (
	"context"
	"fmt"
	"os"

	"astrona/internal/catalog"
	"astrona/internal/labstate"
	"github.com/spf13/cobra"
)

type uiRunRestart struct {
	name, runID string
}

func (r uiRunRestart) message() string {
	return fmt.Sprintf("Lab %s already exists.\n\nDestroy it and start a new lab?\nAll work in this lab and its linked clusters will be lost. Nothing will be saved or submitted before deletion.\n\nThe replacement run is local only. Astrona will not send its progress or results to the Astrona API, even if you are signed in. Other labs stay running.\n\nYes destroys this lab and starts over. No cancels and keeps the existing lab.", sanitizeLine(r.name))
}

// Match the requested source against saved runs without fetching a catalog or
// config. An inherited shell name must never select the lab to destroy.
func findUIRunRestart(cmd *cobra.Command, flags *rootFlags, containers func(context.Context) (map[string][]kindContainer, error)) (*uiRunRestart, error) {
	selected := *flags
	selected.gitExplicit = cmd.Flags().Changed("git")
	applyCurrentLab(cmd, &selected)
	args := cmd.Flags().Args()
	var source *labstate.Source
	if len(args) > 0 && !selected.gitExplicit && catalog.LooksLikeLabID(args[0]) {
		if _, err := os.Stat(args[0]); os.IsNotExist(err) {
			source = &labstate.Source{Catalog: args[0]}
		}
	}
	if source == nil {
		useLabArg(args, &selected)
		source = rememberedSource(&selected)
	}
	names, err := labstate.Names()
	if err != nil {
		return nil, err
	}
	var candidates []uiRunRestart
	for _, name := range names {
		st, err := labstate.Load(name)
		if err != nil {
			return nil, err
		}
		if st != nil && sameSource(st.Source, source) {
			candidates = append(candidates, uiRunRestart{name, workspaceRunID(st)})
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	live, err := containers(cmd.Context())
	if err != nil {
		return nil, nil // The runtime check after config loading reports engine failures.
	}
	var found *uiRunRestart
	for _, candidate := range candidates {
		if len(live[candidate.name]) == 0 {
			continue
		}
		if found != nil {
			return nil, nil // Resolve the config name before asking about an ambiguous source.
		}
		found = &candidate
	}
	return found, nil
}

func approveUIRestart(cmd *cobra.Command, flags *rootFlags, name string, confirm func(*cobra.Command, uiRunNotice) (bool, error)) error {
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	current := uiRunRestart{name, workspaceRunID(st)}
	if flags.uiRestart != nil && *flags.uiRestart != current {
		return fmt.Errorf("the lab changed after the startup notice; nothing was destroyed. Run the command again to review %s", name)
	}
	if flags.uiRestart == nil {
		notice := uiRunNotice{title: "RESTART LAB", summary: current.message(), details: current.message(), detailsTitle: "RESTART DETAILS"}
		yes, err := confirm(cmd, notice)
		if err != nil {
			return err
		}
		if !yes {
			return errUIRunDeclined
		}
		flags.uiRestart = &current
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	flags.localOnly = true
	return nil
}

// Detach the old account session locally before deletion. Persist the upload
// block first so a failed teardown or a later CLI invocation cannot send results.
func discardUIRun(name string, approved *uiRunRestart) error {
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	if approved == nil || approved.name != name || approved.runID != workspaceRunID(st) {
		return fmt.Errorf("the run changed before deletion; nothing was destroyed")
	}
	if st == nil {
		st = &labstate.State{}
	}
	oldSession := st.Session
	st.Session, st.LocalOnly = nil, true
	st.StartedAt = ""
	if err := labstate.Save(name, st); err != nil {
		return err
	}
	if oldSession.IsPlayground() {
		stopWatchdog(oldSession.WatchdogPID, name)
	}
	return nil
}
