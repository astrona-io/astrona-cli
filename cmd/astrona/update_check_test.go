package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateStateTiming(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if !(updateState{}).dueForCheck(now) {
		t.Error("never checked: due")
	}
	if (updateState{CheckedAt: now.Add(-2 * time.Hour)}).dueForCheck(now) {
		t.Error("checked 2h ago: not due (once a day)")
	}
	if !(updateState{CheckedAt: now.Add(-25 * time.Hour)}).dueForCheck(now) {
		t.Error("checked 25h ago: due")
	}
	if !(updateState{CheckedAt: now.Add(-2 * time.Hour), Failed: true}).dueForCheck(now) {
		t.Error("failed 2h ago: retry after an hour")
	}

	newer := updateState{Latest: "v0.2.3"}
	if !newer.dueForNotice("v0.2.2", now) {
		t.Error("newer release, never notified: show")
	}
	newer.NotifiedAt = now.Add(-3 * time.Hour)
	if newer.dueForNotice("v0.2.2", now) {
		t.Error("notified 3h ago: once a day")
	}
	if (updateState{Latest: "v0.2.2"}).dueForNotice("v0.2.2", now) || (updateState{Latest: "v0.2.1"}).dueForNotice("v0.2.2", now) {
		t.Error("not newer: no notice")
	}
}

func TestUpdateStateRoundTripAndOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x", "update-check.json")
	want := updateState{CheckedAt: time.Unix(100, 0).UTC(), Latest: "v0.2.3"}
	saveUpdateState(path, want)
	if got := loadUpdateState(path); !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest {
		t.Errorf("round trip = %+v", got)
	}
	if got := loadUpdateState(filepath.Join(t.TempDir(), "missing.json")); !got.CheckedAt.IsZero() {
		t.Errorf("missing file = %+v", got)
	}
	t.Setenv("CI", "true")
	if updateCheckWanted() {
		t.Error("CI must not check")
	}
}
