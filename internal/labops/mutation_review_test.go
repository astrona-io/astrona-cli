package labops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestLifecycleCommandsHonorMutationLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	release, err := labstate.LockMutation("astro-review")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "review"}}
	for name, action := range map[string]func() error{
		"submit": func() error { _, err := Submit(context.Background(), SubmitRequest{Config: cfg}); return err },
		"stop":   func() error { return Stop(&StopPlan{Lab: "astro-review"}, ui.Discard()) },
		"start":  func() error { _, err := Start("astro-review", ui.Discard()); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := action(); !errors.Is(err, syscall.EWOULDBLOCK) {
				t.Fatalf("expected busy lab before contacting the runtime, got %v", err)
			}
		})
	}
}

func TestStopRejectsReplacementRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	before := &labstate.State{StartedAt: "first"}
	plan := &StopPlan{Lab: "astro-review", runID: RunID(before)}
	if err := labstate.Save(plan.Lab, &labstate.State{StartedAt: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if err := Stop(plan, ui.Discard()); err == nil || !strings.Contains(err.Error(), "restarted elsewhere") {
		t.Fatalf("expected stale plan rejection, got %v", err)
	}
}

func TestLinkedLifecycleLocksPreserveStandaloneRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := cluster.WriteLinks("astro-review", []cluster.LinkState{{Name: "idp", Cluster: "astro-review-idp"}}); err != nil {
		t.Fatal(err)
	}
	if err := labstate.Save("astro-review-idp", &labstate.State{StartedAt: "standalone"}); err != nil {
		t.Fatal(err)
	}
	if release, err := lockLinkedClusters("astro-review", []string{"astro-review-idp"}); err == nil {
		release()
		t.Fatal("accepted a child name claimed by a standalone run")
	}
	release, err := labstate.LockMutation("astro-review-idp")
	if err != nil {
		t.Fatalf("failed ownership check leaked its lock: %v", err)
	}
	release()
}

func TestSubmitKeepsRunLockedUntilResultDelivery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nif [ \"$1\" = ps ]; then printf '%s\\n' astro-review-control-plane; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Every recorded submit deletes the lab: the delete succeeds here.
	if err := os.WriteFile(filepath.Join(bin, "kind"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
	base := t.TempDir()
	lab := LabSource{Config: base}
	before := &labstate.State{StartedAt: "first", Source: RememberedSource(lab), LocalOnly: true}
	if err := labstate.Save("astro-review", before); err != nil {
		t.Fatal(err)
	}
	graded := false
	res, err := Submit(context.Background(), SubmitRequest{
		Config: &config.LabConfig{Metadata: config.MetadataConfig{Name: "review"},
			Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Type: "command", Name: "pass", Command: "true"}}}},
		BaseDir: base, Lab: lab, Status: io.Discard, Warn: io.Discard,
		Destroy: DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard},
		UI:      ui.Options{Plain: true, Screen: io.Discard, Raw: io.Discard},
		Graded: func(_ *SubmitResult, _ *ui.Reporter) error {
			graded = true
			release, err := labstate.LockMutation("astro-review")
			if err == nil {
				defer release()
				// This is where a concurrent reset would save its new session.
				return errors.New("reset acquired the lab between recording and delivery")
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				return err
			}
			return nil
		},
	})
	if err != nil || !graded || res == nil || !res.Recorded {
		t.Fatalf("submit = %+v, graded = %v, error = %v", res, graded, err)
	}
	release, err := labstate.LockMutation("astro-review")
	if err != nil {
		t.Fatalf("submit retained mutation lock: %v", err)
	}
	release()
}
