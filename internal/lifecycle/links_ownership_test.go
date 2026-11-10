package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestRefreshLinkNamesRespectsOwnershipAndExistingLocks(t *testing.T) {
	for _, state := range []string{"standalone", "busy", "already locked", "owned"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "commands")
			t.Setenv("LINK_DNS_LOG", log)
			t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
			for name, body := range map[string]string{
				"docker":  "#!/bin/sh\ncase \"$1\" in ps) printf '%s\\n' astro-app-control-plane astro-app-idp-control-plane;; inspect) echo 10.0.0.1;; esac\n",
				"kubectl": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$LINK_DNS_LOG\"\nprintf '.:53 {\\n}\\n'\n",
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			if err := cluster.WriteLinks("astro-app", []cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}}); err != nil {
				t.Fatal(err)
			}
			releaseParent, err := labstate.LockMutation("astro-app")
			if err != nil {
				t.Fatal(err)
			}
			defer releaseParent()
			var locked []string
			var releaseChild func()
			switch state {
			case "standalone":
				if err := labstate.Save("astro-app-idp", &labstate.State{StartedAt: "replacement"}); err != nil {
					t.Fatal(err)
				}
			case "busy", "already locked":
				releaseChild, err = labstate.LockMutation("astro-app-idp")
				if err != nil {
					t.Fatal(err)
				}
				defer releaseChild()
				if state == "already locked" {
					locked = []string{"astro-app-idp"}
				}
			}
			rep := ui.Discard()
			var warnings []string
			rep.SetObserver(func(event ui.Event) {
				if event.Kind == "warn" {
					warnings = append(warnings, event.Label)
				}
			})
			RefreshLinkNamesLocked("astro-app", false, locked, rep)
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(data)
			want := state == "owned" || state == "already locked"
			if got := strings.Contains(strings.Join(warnings, "\n"), "skipping DNS updates for linked cluster astro-app-idp"); got == want {
				t.Fatalf("skipped child warning = %v, want %v: %v", got, !want, warnings)
			}
			if !strings.Contains(commands, "--context kind-astro-app ") {
				t.Fatalf("parent DNS was not refreshed: %s", commands)
			}
			if got := strings.Contains(commands, "--context kind-astro-app-idp"); got != want {
				t.Fatalf("child DNS touched = %v, want %v: %s", got, want, commands)
			}
			if got := strings.Contains(commands, "idp.astrona.internal"); got != want {
				t.Fatalf("child DNS route published = %v, want %v: %s", got, want, commands)
			}
			if releaseChild != nil {
				releaseChild()
			}
			unlock, err := labstate.LockMutation("astro-app-idp")
			if err != nil {
				t.Fatalf("DNS refresh leaked a child lock: %v", err)
			}
			unlock()
		})
	}
}
