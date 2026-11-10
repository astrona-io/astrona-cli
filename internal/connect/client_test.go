package connect

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"astrona/internal/account"
	"github.com/coder/websocket"
)

func TestRevokedConnectionPreservesReplacementLogin(t *testing.T) {
	for _, tc := range []struct {
		name, site, device, token string
		remove                    bool
	}{
		{"same sign-in", "https://astrona.io", "device-old", "refresh-old", true},
		{"rotated tokens", "https://astrona.io", "device-old", "refresh-new", true},
		{"replacement login", "https://astrona.io", "device-new", "refresh-new", false},
		{"another site", "https://other.example", "device-old", "refresh-new", false},
		{"legacy replacement", "https://astrona.io", "", "refresh-new", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &account.Credentials{Site: "https://astrona.io", DeviceID: "device-old", AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: time.Now().Add(time.Hour)}
			saved := *old
			saved.Site, saved.DeviceID, saved.RefreshToken = tc.site, tc.device, tc.token
			store := account.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
			if err := store.Save(&saved); err != nil {
				t.Fatal(err)
			}
			c := &client{o: Options{Store: store}}
			_, err := c.ended(context.Background(), old, nil, websocket.CloseError{Code: websocket.StatusCode(CloseRevoked)}, nil)
			if !errors.Is(err, ErrRevoked) {
				t.Fatalf("connection end: %v", err)
			}
			remaining, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if (remaining == nil) != tc.remove {
				t.Fatalf("credentials removed=%v, want %v", remaining == nil, tc.remove)
			}
			if remaining != nil && (remaining.Site != saved.Site || remaining.RefreshToken != saved.RefreshToken) {
				t.Fatal("replacement credentials changed")
			}
		})
	}
}
