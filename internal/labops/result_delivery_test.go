package labops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/outbox"
)

type resultTransport func(*http.Request) (*http.Response, error)

func (f resultTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeliverResultQueuesWithoutActiveCredentials(t *testing.T) {
	for _, signedOut := range []bool{false, true} {
		name := "expired token offline"
		if signedOut {
			name = "signed out"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			c := account.NewClient()
			c.HTTP = &http.Client{Transport: resultTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
			s, err := account.DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			cr := &account.Credentials{Site: "https://astrona.io", AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(-time.Hour)}
			if !signedOut {
				if err := s.Save(cr); err != nil {
					t.Fatal(err)
				}
			}
			old := AccountDeps
			t.Cleanup(func() { AccountDeps = old })
			AccountDeps = func() (*account.Client, account.Store, string, error) { return c, s, cr.Site, nil }
			src := &labstate.Source{Catalog: "ATS/x"}
			if err := labstate.Save("astro-result", &labstate.State{Source: src, Session: &labstate.Session{Site: cr.Site, ID: "s1"}}); err != nil {
				t.Fatal(err)
			}
			gradedAt := time.Now().UTC()
			got := DeliverResult(context.Background(), io.Discard, io.Discard, "astro-result", src, Submission{Pass: true}, gradedAt)
			pending, err := outbox.PendingFor("s1")
			if err != nil {
				t.Fatal(err)
			}
			if got != ResultQueued || pending != 1 {
				t.Fatalf("outcome=%v pending=%d; want queued and one pending", got, pending)
			}

			cr.AccessExpiresAt = time.Now().Add(time.Hour)
			if err := s.Save(cr); err != nil {
				t.Fatal(err)
			}
			var sent struct {
				Result Submission `json:"result"`
				Key    string     `json:"idempotency_key"`
				At     time.Time  `json:"at"`
			}
			c.HTTP.Transport = resultTransport(func(r *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"id":"r1"}`)), Header: make(http.Header)}, nil
			})
			status, err := FlushOutbox(context.Background(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if status.Pending != 0 || !sent.Result.Pass || sent.Key == "" || !sent.At.Equal(gradedAt) {
				t.Fatalf("flush=%+v result=%+v", status, sent)
			}
		})
	}
}

func TestQueuedResultHonorsUsageOptOut(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		name := "disabled"
		if allowed {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			old := UsageSamplesAllowed
			t.Cleanup(func() { UsageSamplesAllowed = old })
			UsageSamplesAllowed = func() bool { return allowed }
			var payload map[string]json.RawMessage
			c := account.NewClient()
			c.HTTP = &http.Client{Transport: resultTransport(func(r *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"id":"r1"}`)), Header: make(http.Header)}, nil
			})}
			s, err := account.DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			cr := &account.Credentials{Site: "https://astrona.io", AccessToken: "access", RefreshToken: "refresh", AccessExpiresAt: time.Now().Add(time.Hour)}
			if err := s.Save(cr); err != nil {
				t.Fatal(err)
			}
			sender := &SiteSender{Client: c, Store: s, creds: map[string]*account.Credentials{}}
			ev := outbox.Event{Kind: outbox.Result, At: time.Now(), Key: "key", Result: json.RawMessage(`{"pass":true}`), Usage: &account.UsageReport{Samples: 5}}
			if err := outbox.Add(cr.Site, "s1", ev); err != nil {
				t.Fatal(err)
			}
			if _, err := outbox.Flush(context.Background(), sender); err != nil {
				t.Fatal(err)
			}
			if (payload["usage"] != nil) != allowed {
				t.Fatalf("usage allowed=%v, sent=%s", allowed, payload["usage"])
			}
			if payload["result"] == nil {
				t.Fatal("the result was not delivered")
			}
		})
	}
}
