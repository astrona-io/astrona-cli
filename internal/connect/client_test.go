package connect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
	"github.com/coder/websocket"
)

type signInTransport func(*http.Request) (*http.Response, error)

func (f signInTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnauthorizedConnectionReloadsSignIn(t *testing.T) {
	for _, tc := range []struct {
		name, site, device string
		missing, rejected  bool
		reconnect          bool
	}{
		{name: "replacement login", site: "https://astrona.io", device: "new-device", reconnect: true},
		{name: "another site", site: "https://other.example", device: "new-device"},
		{name: "signed out", missing: true},
		{name: "revoked sign-in", site: "https://astrona.io", device: "old-device", rejected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &account.Credentials{Site: "https://astrona.io", DeviceID: "old-device", AccessToken: "old-access", RefreshToken: "old-refresh", AccessExpiresAt: time.Now().Add(time.Hour)}
			saved := *old
			saved.Site, saved.DeviceID = tc.site, tc.device
			if tc.device != old.DeviceID {
				saved.AccessToken, saved.RefreshToken = "new-access", "new-refresh"
			}
			store := account.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
			if !tc.missing {
				if err := store.Save(&saved); err != nil {
					t.Fatal(err)
				}
			}
			api := account.NewClient()
			api.HTTP = &http.Client{Transport: signInTransport(func(r *http.Request) (*http.Response, error) {
				if !tc.rejected {
					t.Fatalf("unexpected request to %s", r.URL)
				}
				status, body := http.StatusBadRequest, `{"error":"invalid_grant"}`
				if r.URL.Path == "/api/cli/config" {
					status, body = http.StatusOK, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token"}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			c := &client{o: Options{Client: api, Store: store, Site: old.Site}}
			lost := func(kind string, _ error, code int) *Disconnect { return &Disconnect{Kind: kind, CloseCode: code} }
			d, err := c.ended(context.Background(), old, nil, websocket.CloseError{Code: websocket.StatusCode(CloseUnauthorized)}, lost)
			if tc.reconnect {
				if err != nil || d == nil || d.Kind != "unauthorized" {
					t.Fatalf("replacement login did not reconnect: disconnect=%+v err=%v", d, err)
				}
			} else if !errors.Is(err, account.ErrSignedOut) || d != nil {
				t.Fatalf("sign-in should stop connection: disconnect=%+v err=%v", d, err)
			}
			remaining, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing || tc.rejected {
				if remaining != nil {
					t.Fatal("rejected or missing sign-in was restored")
				}
			} else if remaining == nil || remaining.Site != saved.Site || remaining.DeviceID != saved.DeviceID || remaining.RefreshToken != saved.RefreshToken {
				t.Fatal("replacement sign-in changed")
			}
		})
	}
}

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
