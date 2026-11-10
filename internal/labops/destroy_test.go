package labops

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestConfigDestroyPreservesActiveTest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("DESTROY_LOG", log)
	for name, body := range map[string]string{
		"docker": "#!/bin/sh\nif [ \"$1\" = ps ]; then printf '%s\\n' astro-review-control-plane astro-test-review-control-plane; fi\n",
		"kind":   "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$DESTROY_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	release, err := labstate.LockMutation("astro-test-review")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = DestroyLab(Teardown{ClusterName: "review", Runtime: config.RuntimeConfig{Type: "kind"}}, t.TempDir(), ui.Discard(), DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "delete cluster --name astro-review\n"; got != want {
		t.Fatalf("runtime commands = %q, want %q; active test must be kept", got, want)
	}
}

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
