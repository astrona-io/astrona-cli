package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
)

type credentialTransport func(*http.Request) (*http.Response, error)

func (f credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoginAndLogoutWaitForRenewal(t *testing.T) {
	for _, operation := range []string{"login", "logout"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			refreshStarted := make(chan struct{})
			finishRefresh := make(chan struct{})
			loginAuthorized := make(chan struct{})
			revoked := make(chan string, 1)
			c := account.NewClient()
			c.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			c.HTTP = &http.Client{Transport: credentialTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]string
				if r.Body != nil {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
				}
				var answer string
				switch r.URL.Path {
				case "/api/cli/config":
					answer = `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`
				case "/api/cli/device":
					answer = `{"device_code":"d","user_code":"ABCD-1234","verification_uri":"https://astrona.io/cli/authorize","verification_uri_complete":"https://astrona.io/cli/authorize?code=ABCD-1234","expires_in":60,"interval":1}`
				case "/api/cli/token":
					if body["grant_type"] == "refresh_token" {
						close(refreshStarted)
						select {
						case <-finishRefresh:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						answer = `{"access_token":"alice-access-next","refresh_token":"alice-refresh-next","token_type":"Bearer","expires_in":300,"username":"alice"}`
					} else {
						close(loginAuthorized)
						answer = `{"access_token":"bob-access","refresh_token":"bob-refresh","token_type":"Bearer","expires_in":300,"username":"bob"}`
					}
				case "/api/cli/revoke":
					revoked <- body["refresh_token"]
					answer = `{}`
				default:
					return nil, fmt.Errorf("unexpected endpoint %s", r.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(answer)), Header: make(http.Header)}, nil
			})}
			s := account.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
			old := &account.Credentials{Site: "https://astrona.io", AccessToken: "alice-access", RefreshToken: "alice-refresh", Username: "alice", AccessExpiresAt: time.Now().Add(-time.Hour)}
			if err := s.Save(old); err != nil {
				t.Fatal(err)
			}
			renewed := make(chan error, 1)
			go func() { _, err := c.Renew(ctx, s, old); renewed <- err }()
			select {
			case <-refreshStarted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			changed := make(chan error, 1)
			go func() {
				if operation == "login" {
					changed <- loginWithBrowser(ctx, c, s, old.Site, testDevice, false)
				} else {
					changed <- logout(c, s)
				}
			}()
			if operation == "login" {
				select {
				case <-loginAuthorized:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			// Renewal still owns the old token. Neither command may replace or
			// remove it, or revoke it, until the new refresh token is saved.
			select {
			case err := <-changed:
				t.Fatalf("%s completed during renewal: %v", operation, err)
			case token := <-revoked:
				t.Fatalf("%s revoked %q during renewal", operation, token)
			case <-time.After(50 * time.Millisecond):
			}
			close(finishRefresh)
			if err := <-renewed; err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatal(err)
			}
			if token := <-revoked; token != "alice-refresh-next" {
				t.Errorf("revoked %q, want current token", token)
			}
			saved, err := s.Load()
			if err != nil {
				t.Fatal(err)
			}
			if operation == "logout" {
				if saved != nil {
					t.Fatal("renewal restored credentials after logout")
				}
			} else if saved == nil || saved.Username != "bob" {
				t.Fatal("renewal replaced the newly completed login")
			}
		})
	}
}
