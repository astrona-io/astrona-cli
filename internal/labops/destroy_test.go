package labops

import (
	"io"
	"strings"
	"testing"

	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestDiscoveryDestroyPreservesRunChangedSinceDiscovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no real runtime can be reached by these tests
	st := &labstate.State{StartedAt: "first run"}
	expected := RunID(st)
	st.StartedAt = "replacement"
	if err := labstate.Save("astro-lab", st); err != nil {
		t.Fatal(err)
	}
	err := destroyDiscoveredLab("astro-lab", expected, ui.Discard(), DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "changed during discovery") {
		t.Fatalf("expected identity rejection, got %v", err)
	}
	got, err := labstate.Load("astro-lab")
	if err != nil || got == nil || got.StartedAt != "replacement" {
		t.Fatalf("replacement state changed: %v, %v", got, err)
	}
}

func TestDiscoveryDestroyHonorsMutationLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	release, err := labstate.LockMutation("astro-lab")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = destroyDiscoveredLab("astro-lab", "", ui.Discard(), DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "being changed") {
		t.Fatalf("expected mutation-lock rejection, got %v", err)
	}
}
