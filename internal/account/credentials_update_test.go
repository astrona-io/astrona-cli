package account

import (
	"context"
	"os"
	"testing"
	"time"

	"astrona/internal/apphome"
)

func TestSavedLoginSurvivesBuildUpdate(t *testing.T) {
	previousVersion := "v1.0.0"
	if apphome.Development() {
		previousVersion = "developer"
	}
	t.Cleanup(func() { apphome.Configure(previousVersion) })
	for _, tc := range []struct{ name, before, after, otherProfile string }{
		{"release upgrade", "v0.4.0", "v0.5.0", "developer"},
		{"development rebuild", "developer", "developer", "v0.5.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			site := newFakeSite(t)
			apphome.Configure(tc.before)
			store, err := DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			creds := testCreds(site, time.Now().Add(-time.Hour))
			if err := store.Save(creds); err != nil {
				t.Fatal(err)
			}
			apphome.Configure(tc.after)
			reopened, err := DefaultStore()
			if err != nil || reopened.Path != store.Path {
				t.Fatalf("update changed credential path: %q, %v", reopened.Path, err)
			}
			// A new process can renew the old login without browser authorization.
			active, err := testClient(nil).Active(context.Background(), reopened, site.URL)
			if err != nil {
				t.Fatal(err)
			}
			if active.Username != creds.Username || active.RefreshToken != "refresh-r1" || !active.fresh(time.Now()) {
				t.Fatal("saved login was not renewed after updating")
			}
			site.mu.Lock()
			calls, devices := site.refreshCalls, len(site.devices)
			site.mu.Unlock()
			if calls != 1 || devices != 0 {
				t.Fatalf("refresh calls = %d, browser authorizations = %d", calls, devices)
			}
			before, err := os.ReadFile(store.Path)
			if err != nil {
				t.Fatal(err)
			}
			apphome.Configure(tc.otherProfile)
			otherStore, err := DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			if other, err := otherStore.Load(); err != nil || other != nil {
				t.Fatalf("other profile inherited the login: %v", err)
			}
			apphome.Configure(tc.after)
			restored, err := DefaultStore()
			if err != nil || restored.Path != store.Path {
				t.Fatalf("switching back changed credential path: %q, %v", restored.Path, err)
			}
			after, err := os.ReadFile(restored.Path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("switching profiles changed the saved login: %v", err)
			}
		})
	}
}
