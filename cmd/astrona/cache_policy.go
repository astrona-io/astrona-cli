package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"time"

	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

func cacheCleanupWanted(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden {
			return false
		}
	}
	// Cleanup commands stay usable without review. They must not apply
	// unreviewed cache defaults as an automatic side effect.
	return !settingsReviewExempt(cmd)
}

// Automatic maintenance runs only after command setup succeeded and the shared
// lock was released. Failures leave the command's original result unchanged.
func enforceCachePolicy(cmd *cobra.Command, s localdata.Store) {
	if !cacheCleanupWanted(cmd) || cmd.Context().Err() != nil {
		return
	}
	err := pruneCache(cmd.Context(), cmd, s)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return // A UI or another command still uses the cache; try on a later command.
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
	policy, err := loadCachePolicy(filepath.Join(s.Data, "settings.json"))
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
