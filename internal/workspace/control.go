package workspace

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The control channel lets the CLI (astrona ui status, astrona ui stop) talk
// to the running server. It proves itself with a key derived from the
// secret in server.json, which only the user can read; the page never has
// it. Browsers always send Origin on these methods, and the control routes
// refuse any request that carries one.

const controlHeader = "X-Astrona-Control"

func controlKey(secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("astrona-workspace-control-v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) requireControl(next http.HandlerFunc) http.HandlerFunc {
	want := controlKey(s.auth.secret)
	return func(w http.ResponseWriter, r *http.Request) {
		if _, hasOrigin := r.Header["Origin"]; hasOrigin {
			writeError(w, http.StatusForbidden, "control requests come from the CLI only")
			return
		}
		got := r.Header.Get(controlHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid control key")
			return
		}
		next(w, r)
	}
}

// ControlStatus is what astrona ui status reports about a running server.
type ControlStatus struct {
	PID       int            `json:"pid"`
	Port      int            `json:"port"`
	StartedAt time.Time      `json:"startedAt"`
	Version   string         `json:"version,omitempty"`
	Clients   int            `json:"clients"`
	Terminals []TerminalInfo `json:"terminals"`
}

func (s *Server) controlStatus() ControlStatus {
	info := s.Info()
	views := []TerminalInfo{}
	for _, t := range s.terms.list("") {
		v := t.view()
		v.Lab = t.lab
		views = append(views, v)
	}
	return ControlStatus{PID: info.PID, Port: info.Port, StartedAt: info.StartedAt, Version: info.Version,
		Clients: int(s.held.Load()), Terminals: views}
}

func (s *Server) handleControlStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.controlStatus())
}

func (s *Server) handleControlStop(w http.ResponseWriter, _ *http.Request) {
	s.Stop()
	writeJSON(w, http.StatusAccepted, map[string]bool{"stopping": true})
}

// Stop asks Serve to shut down: it closes every terminal and returns nil.
func (s *Server) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// controlClient is a short-lived client for the control routes. It never
// uses a proxy: the server is on loopback.
var controlClient = &http.Client{
	Timeout:   5 * time.Second,
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
}

func (i ServerInfo) control(ctx context.Context, method, path string, out any) error {
	if !i.Alive() {
		return ErrServerNotRunning
	}
	secret, err := b64.DecodeString(i.Secret)
	if err != nil {
		return errors.New("server.json has an invalid secret")
	}
	if i.Port <= 0 || i.Port > 65535 {
		return fmt.Errorf("server.json has an invalid port %d", i.Port)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://%s:%d%s", loopbackAddr, i.Port, path), nil)
	if err != nil {
		return err
	}
	req.Header.Set(controlHeader, controlKey(secret))
	resp, err := controlClient.Do(req)
	if err != nil {
		return fmt.Errorf("workspace server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("workspace server answered %s: %s", resp.Status, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Status asks the running server for its clients and terminals.
func (i ServerInfo) Status(ctx context.Context) (ControlStatus, error) {
	var st ControlStatus
	err := i.control(ctx, http.MethodGet, "/control/v1/status", &st)
	return st, err
}

// Stop asks the running server to close its terminals and exit. Labs keep
// running.
func (i ServerInfo) Stop(ctx context.Context) error {
	return i.control(ctx, http.MethodPost, "/control/v1/stop", nil)
}
