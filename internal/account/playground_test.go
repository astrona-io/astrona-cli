package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlaygroundSessionAndStop(t *testing.T) {
	ctx := context.Background()
	var created map[string]any
	var stops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/cli/lab-sessions":
			json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"s-1","token":"t","lab":"ATS014/section-010/module-01/playground","url":"http://x/labs/a","max_minutes":90,"kind":"playground","deadline_at":"2026-10-08T12:00:00Z"}`))
		case r.URL.Path == "/api/cli/lab-sessions/s-1/stop":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			stops = append(stops, r.Header.Get("Authorization")+" "+body["reason"])
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/cli/lab-sessions/s-1/renew":
			w.Write([]byte(`{"previous_id":"s-1","banked_seconds":600,"id":"s-2","expires_at":"2026-10-09T12:00:00Z","deadline_at":"2026-10-08T14:00:00Z","max_minutes":90}`))
		case r.URL.Path == "/api/cli/lab-sessions/done/renew":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"statusMessage":"This playground has already stopped"}`))
		case r.URL.Path == "/api/cli/lab-sessions/gone/stop":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"statusMessage":"Session not found"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	cr := &Credentials{Site: srv.URL, AccessToken: "access-0", RefreshToken: "r", AccessExpiresAt: time.Now().Add(time.Hour)}
	s.Save(cr)

	ls, err := testClient(nil).CreateLabSession(ctx, s, cr, "ATS014/section-010/module-01/playground",
		SessionOptions{Kind: "playground", TimeLimitMinutes: 90})
	if err != nil || ls.Kind != "playground" || ls.DeadlineAt == "" || ls.MaxMinutes != 90 {
		t.Fatalf("session = %+v, %v", ls, err)
	}
	if created["kind"] != "playground" || created["time_limit_minutes"] != float64(90) {
		t.Errorf("request = %v", created)
	}

	if err := testClient(nil).StopLabSession(ctx, s, cr, "s-1", "time_limit"); err != nil {
		t.Fatal(err)
	}
	if len(stops) != 1 || stops[0] != "Bearer access-0 time_limit" {
		t.Errorf("stops = %v", stops)
	}
	if err := testClient(nil).StopLabSession(ctx, s, cr, "gone", "destroyed"); !errors.Is(err, ErrSessionGone) {
		t.Errorf("gone: %v", err)
	}
	if err := testClient(nil).StopLabSession(ctx, s, cr, "a/b", "destroyed"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("bad id: %v", err)
	}

	renewed, err := testClient(nil).RenewLabSession(ctx, s, cr, "s-1")
	if err != nil || renewed.ID != "s-2" || renewed.BankedSeconds != 600 || renewed.DeadlineAt != "2026-10-08T14:00:00Z" || renewed.MaxMinutes != 90 {
		t.Fatalf("renew = %+v, %v", renewed, err)
	}
	if _, err := testClient(nil).RenewLabSession(ctx, s, cr, "done"); !errors.Is(err, ErrPlaygroundStopped) {
		t.Errorf("stopped: %v", err)
	}
}
