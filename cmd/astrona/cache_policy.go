package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"astrona/internal/localdata"
	"astrona/internal/settings"
	"github.com/spf13/cobra"
)

// cacheCleanupWanted limits automatic cleanup to the commands that fill the
// caches: run and test download images, addons and Git checkouts. Every other
// command skips it, so cleanup is rare and never delays unrelated commands.
func cacheCleanupWanted(cmd *cobra.Command) bool {
	if cmd.Hidden || settingsReviewExempt(cmd) || (cmd.HasParent() && cmd.Parent().HasParent()) {
		return false // only the top-level run and test
	}
	return cmd.Name() == "run" || cmd.Name() == "test"
}

// Automatic maintenance runs only after command setup succeeded and the shared
// lock was released. It never waits: if any other command holds the lock it is
// skipped and tried after a later run. Failures leave the command's original
// result unchanged.
func enforceCachePolicy(cmd *cobra.Command, s localdata.Store) {
	if !cacheCleanupWanted(cmd) || cmd.Context().Err() != nil {
		return
	}
	err := pruneCache(cmd.Context(), cmd, s)
	if errors.Is(err, localdata.ErrBusy) {
		return // A UI or another command still uses the cache; try after a later run.
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Cache limits deferred: %v. Retry with astrona data clean --policy.\n", err)
	}
}

func pruneCache(ctx context.Context, cmd *cobra.Command, s localdata.Store) error {
	release, err := s.Lock(true)
	if err != nil {
		return err
	}
	defer release()
	policy, err := settings.LoadCachePolicy(filepath.Join(s.Data, "settings.json"))
	if err != nil {
		return err
	}
	p, err := s.PlanPolicy(ctx, policy, time.Now())
	if err != nil {
		return err
	}
	err = p.Apply(ctx)
	if p.Removed > 0 {
		var size int64
		for _, item := range p.Items[:p.Removed] {
			size += item.Bytes
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Cache limits: removed %d disposable entries (%s); downloads can be fetched again.\n", p.Removed, formatBytes(size))
	}
	for _, note := range p.Skipped {
		fmt.Fprintln(cmd.ErrOrStderr(), "Cache limits deferred:", note)
	}
	return err
}
