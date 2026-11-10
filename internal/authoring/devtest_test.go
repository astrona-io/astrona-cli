package authoring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestRunTestHonorsMutationLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	name := config.NormalizeTestClusterName("busy")
	release, err := labstate.LockMutation(name)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	run := RunTest(context.Background(), &config.LabConfig{}, t.TempDir(), TestOptions{ClusterName: name}, ui.Discard())
	if run.Err == nil || !strings.Contains(run.Err.Error(), "being changed") {
		t.Fatalf("test ignored mutation lock: %v", run.Err)
	}
}

// A run whose context is already cancelled creates nothing: it stops right
// after the clean-slate teardown with ErrTestInterrupted.
func TestRunTestStopsWhenInterrupted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no kind/docker/podman: nothing real can run
	rep, err := ui.NewReporter("test", "sig", false)
	if err != nil {
		t.Fatal(err)
	}
	defer rep.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "sig"}}
	run := RunTest(ctx, cfg, t.TempDir(), TestOptions{ClusterName: config.NormalizeTestClusterName("sig")}, rep)
	if !errors.Is(run.Err, ErrTestInterrupted) || run.Pass {
		t.Fatalf("got pass=%t err=%v, want ErrTestInterrupted", run.Pass, run.Err)
	}
}
