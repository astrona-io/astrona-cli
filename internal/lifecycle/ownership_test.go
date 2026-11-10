package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func fakeOwnershipRuntime(t *testing.T, containers string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("OWNERSHIP_CONTAINERS", containers)
	t.Setenv("OWNERSHIP_LOG", log)
	for name, body := range map[string]string{
		"docker":  "#!/bin/sh\nif [ \"$1\" = ps ]; then printf '%s\\n' \"$OWNERSHIP_CONTAINERS\"; fi\n",
		"kind":    "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$OWNERSHIP_LOG\"\nexit 1\n",
		"kubectl": "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return log
}

func TestLinkedSetupRejectsNameCollisionBeforeRecordingOwnership(t *testing.T) {
	for _, parallel := range []int{1, 2} {
		t.Run(strconv.Itoa(parallel), func(t *testing.T) {
			log := fakeOwnershipRuntime(t, "astro-app-idp-control-plane")
			cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{
				Clusters: []config.KindCluster{{Name: "db"}, {Name: "idp"}},
			}}}
			_, err := StartLinkedClusters(context.Background(), cfg, t.TempDir(), "astro-app", false, parallel, ui.Discard())
			if err == nil || !strings.Contains(err.Error(), "conflicts") {
				t.Fatalf("collision error = %v", err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("kind was called before collision rejection: %v", err)
			}
			if saved, err := cluster.ReadLinks("astro-app"); err != nil || len(saved) != 0 {
				t.Fatalf("collision claimed ownership: %v, %v", saved, err)
			}
			// The first child's reservation is released when a later one conflicts.
			release, err := labstate.LockMutation("astro-app-db")
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}

func TestLinkedReservationRejectsAnotherParentsName(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			containers := ""
			if existing {
				containers = "astro-app-foo-bar-control-plane"
			}
			log := fakeOwnershipRuntime(t, containers)
			ours := []cluster.LinkState{{Name: "foo-bar", Cluster: "astro-app-foo-bar"}}
			other := []cluster.LinkState{{Name: "bar", Cluster: "astro-app-foo-bar"}}
			// An old ownership entry must not authorize replacing another
			// parent's child, even while that child's reset has left it absent.
			if err := cluster.WriteLinks("astro-app", ours); err != nil {
				t.Fatal(err)
			}
			if err := cluster.WriteLinks("astro-app-foo", other); err != nil {
				t.Fatal(err)
			}
			if release, err := reserveLinkedClusters("astro-app", ours); err == nil {
				release()
				t.Fatal("claimed another parent's linked name")
			}
			if got := OwnedClusters("astro-app", nil); len(got) != 0 {
				t.Fatalf("ambiguous ownership allowed deletion: %v", got)
			}
			DestroyOwnedClusters("astro-app", []string{"astro-app-foo-bar"}, ui.Discard())
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("destroyed another parent's child: %v", err)
			}
		})
	}
}

func TestLinkedResetOwnershipAllowsMissingChild(t *testing.T) {
	fakeOwnershipRuntime(t, "")
	if err := cluster.WriteLinks("astro-app", []cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}}); err != nil {
		t.Fatal(err)
	}
	if err := RequireLinkedOwnership("astro-app", "astro-app-idp"); err != nil {
		t.Fatalf("missing child must be recreatable from saved ownership: %v", err)
	}
	if err := RequireLinkedOwnership("astro-app", "astro-app-unrelated"); err == nil {
		t.Fatal("reset accepted a name without saved ownership")
	}
}

func TestSoftResetRejectsUnownedLinkedCluster(t *testing.T) {
	fakeOwnershipRuntime(t, "astro-app-control-plane\nastro-app-idp-control-plane")
	cfg := &config.LabConfig{Runtime: config.RuntimeConfig{Kind: &config.KindConfig{
		Clusters: []config.KindCluster{{Name: "idp"}},
	}}}
	if err := softResetClusters(cfg, t.TempDir(), "astro-app", "idp", ui.Discard()); err == nil || !strings.Contains(err.Error(), "no saved ownership") {
		t.Fatalf("soft reset accepted config-only ownership: %v", err)
	}
	release, err := labstate.LockMutation("astro-app-idp")
	if err != nil {
		t.Fatalf("failed soft reset leaked child lock: %v", err)
	}
	release()
}

func TestLinkedReservationHonorsSavedOwnershipAndStandaloneRuns(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy linked state", true: "standalone replacement"}[standalone], func(t *testing.T) {
			fakeOwnershipRuntime(t, "astro-app-idp-control-plane")
			links := []cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}}
			if err := cluster.WriteLinks("astro-app", links); err != nil {
				t.Fatal(err)
			}
			if standalone {
				if err := labstate.Save("astro-app-idp", &labstate.State{StartedAt: "new standalone run"}); err != nil {
					t.Fatal(err)
				}
			}
			release, err := reserveLinkedClusters("astro-app", links)
			if standalone {
				if err == nil {
					release()
					t.Fatal("claimed a standalone run using stale ownership")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if unexpected, err := labstate.LockMutation("astro-app-idp"); err == nil {
				unexpected()
				t.Fatal("linked setup did not hold the child's mutation lock")
			}
		})
	}
}

func TestOwnedClustersRequiresSavedOwnership(t *testing.T) {
	fakeOwnershipRuntime(t, "astro-app-idp-control-plane")
	labs := []config.KindCluster{{Name: "idp"}}
	if got := OwnedClusters("astro-app", labs); len(got) != 0 {
		t.Fatalf("config claimed unrelated cluster: %v", got)
	}
	if err := cluster.WriteLinks("astro-app", []cluster.LinkState{{Name: "idp", Cluster: "astro-app-idp"}}); err != nil {
		t.Fatal(err)
	}
	if got := OwnedClusters("astro-app", labs); len(got) != 1 {
		t.Fatalf("saved linked cluster missing: %v", got)
	}
	if err := labstate.Save("astro-app-idp", &labstate.State{StartedAt: "standalone"}); err != nil {
		t.Fatal(err)
	}
	if got := OwnedClusters("astro-app", labs); len(got) != 0 {
		t.Fatalf("stale links claimed standalone replacement: %v", got)
	}
}

func TestLinkedDestroyPreservesBusyOrReplacedChild(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement", true: "busy"}[busy], func(t *testing.T) {
			log := fakeOwnershipRuntime(t, "astro-app-idp-control-plane")
			if busy {
				release, err := labstate.LockMutation("astro-app-idp")
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			} else if err := labstate.Save("astro-app-idp", &labstate.State{StartedAt: "replacement"}); err != nil {
				t.Fatal(err)
			}
			DestroyOwnedClusters("astro-app", []string{"astro-app-idp"}, ui.Discard())
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("destroyed protected linked name: %v", err)
			}
		})
	}
}
