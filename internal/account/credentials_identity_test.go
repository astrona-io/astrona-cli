package account

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRenewRejectsReplacementLogin(t *testing.T) {
	for _, expires := range []time.Duration{-time.Hour, time.Hour} {
		t.Run(expires.String(), func(t *testing.T) {
			f := newFakeSite(t)
			c := testClient(nil)
			s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
			stale := testCreds(f, time.Now().Add(-time.Hour))
			if err := s.Save(stale); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			release, err := s.Lock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := c.Renew(ctx, s, stale); done <- err }()
			replacement := testCreds(f, time.Now().Add(expires))
			replacement.DeviceID = "another-device"
			replacement.AccessToken, replacement.RefreshToken = "new-access", "new-refresh"
			if err := s.Save(replacement); err != nil {
				release()
				t.Fatal(err)
			}
			release()
			select {
			case err := <-done:
				if !errors.Is(err, ErrSignedOut) {
					t.Fatalf("renewal with a replaced sign-in: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			saved, err := s.Load()
			if err != nil {
				t.Fatal(err)
			}
			if saved == nil || saved.RefreshToken != replacement.RefreshToken || saved.DeviceID != replacement.DeviceID {
				t.Fatal("renewal changed the replacement sign-in")
			}
			if f.refreshCalls != 0 {
				t.Fatal("renewal sent the replacement account's token")
			}
		})
	}
}
