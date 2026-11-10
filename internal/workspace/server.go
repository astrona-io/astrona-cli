package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"astrona/internal/workspace/web"
)

// loopbackAddr is the only address the server ever listens on. It is a
// constant on purpose: Config has no field that could widen it.
const loopbackAddr = "127.0.0.1"

const (
	maxJSONBody     = 4 << 10
	shutdownTimeout = 5 * time.Second
)

// ErrIdle is returned by Serve when the server stopped because it was idle.
var ErrIdle = errors.New("workspace server idle")

// Config configures a Server. The zero value is usable.
type Config struct {
	// Port is the preferred port on 127.0.0.1. When it is 0 or already in
	// use, the server takes a free port instead.
	Port int
	// Version and Profile are reported by GET /api/v1/server.
	Version string
	Profile string
	// IdleTimeout stops Serve after this long with no requests, no held
	// connections and Busy reporting false. Zero never stops.
	IdleTimeout time.Duration
	// Busy reports work that keeps the server alive, such as running labs.
	Busy func() bool
	// Assets is the frontend; nil uses the embedded web.FS().
	Assets fs.FS
}

// Server is the loopback workspace server. Create it with New, then call
// Serve.
type Server struct {
	cfg       Config
	ln        net.Listener
	port      int
	srv       *http.Server
	auth      *auth
	origins   origins
	assets    fs.FS
	startedAt time.Time

	lastActive atomic.Int64 // unix nanoseconds
	held       atomic.Int64 // long-lived connections, see Hold

	// life is the server's lifetime. Terminals run under it, not under a
	// request context, and it ends after they are closed.
	life       context.Context
	lifeCancel context.CancelFunc
	runMu      sync.Mutex // serialises Start
	terms      *terminals
	subs       submits
	stop       chan struct{}
	stopOnce   sync.Once
}

// New binds 127.0.0.1 and prepares the server. Nothing is served until Serve.
func New(cfg Config) (*Server, error) {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("invalid workspace port %d", cfg.Port)
	}
	// Every start gets a fresh secret. The record of exchanged codes lives
	// only in memory, so reusing a secret across a restart would let an
	// already-exchanged code be exchanged again.
	secret, err := NewSecret()
	if err != nil {
		return nil, err
	}
	ln, err := listenLoopback(cfg.Port)
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	assets := cfg.Assets
	if assets == nil {
		assets = web.FS()
	}
	s := &Server{
		cfg:       cfg,
		ln:        ln,
		port:      port,
		auth:      newAuth(secret, time.Now),
		origins:   newOrigins(port),
		assets:    assets,
		startedAt: time.Now(),
		terms:     newTerminals(),
		subs:      submits{jobs: map[string]*submitJob{}},
		stop:      make(chan struct{}),
	}
	s.life, s.lifeCancel = context.WithCancel(context.Background())
	s.touch()
	s.srv = &http.Server{
		Handler:           s.guard(s.routes()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	return s, nil
}

// listenLoopback listens on 127.0.0.1:port, falling back to a free port.
func listenLoopback(port int) (net.Listener, error) {
	if port != 0 {
		ln, err := net.Listen("tcp4", net.JoinHostPort(loopbackAddr, fmt.Sprint(port)))
		if err == nil {
			return ln, nil
		}
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(loopbackAddr, "0"))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", loopbackAddr, err)
	}
	return ln, nil
}

// Port returns the port the server listens on.
func (s *Server) Port() int { return s.port }

// Info describes this server for server.json.
func (s *Server) Info() ServerInfo {
	return ServerInfo{
		Schema:    serverInfoSchema,
		PID:       os.Getpid(),
		Port:      s.port,
		StartedAt: s.startedAt.UTC(),
		Version:   s.cfg.Version,
		Build:     ThisBuild(),
		Secret:    b64.EncodeToString(s.auth.secret),
	}
}

// LaunchURL returns the page URL with a fresh single-use code.
func (s *Server) LaunchURL(ttl time.Duration) (string, error) {
	return s.Info().LaunchURL(ttl)
}

// Hold marks a long-lived connection (WebSocket, event stream) so the
// server does not go idle while it is open. Call the returned func once when
// it closes.
func (s *Server) Hold() (release func()) {
	s.held.Add(1)
	var once atomic.Bool
	return func() {
		if once.CompareAndSwap(false, true) {
			s.held.Add(-1)
			s.touch()
		}
	}
}

func (s *Server) touch() { s.lastActive.Store(time.Now().UnixNano()) }

func (s *Server) idle(now time.Time) bool {
	if s.cfg.IdleTimeout <= 0 || s.held.Load() > 0 {
		return false
	}
	if now.Sub(time.Unix(0, s.lastActive.Load())) < s.cfg.IdleTimeout {
		return false
	}
	if s.terms.running() || runsBusy() {
		return false
	}
	return s.cfg.Busy == nil || !s.cfg.Busy()
}

// Serve serves until ctx is done or Stop is called (returning nil), the
// server goes idle (returning ErrIdle) or the listener fails. Every terminal
// is closed before it returns; labs keep running.
func (s *Server) Serve(ctx context.Context) error {
	defer s.lifeCancel()
	defer s.terms.closeAll()
	errc := make(chan error, 1)
	go func() { errc <- s.srv.Serve(s.ln) }()

	var tick <-chan time.Time
	if s.cfg.IdleTimeout > 0 {
		t := time.NewTicker(min(max(s.cfg.IdleTimeout/4, time.Second), 30*time.Second))
		defer t.Stop()
		tick = t.C
	}
	var result error
loop:
	for {
		select {
		case err := <-errc:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("workspace server: %w", err)
		case <-ctx.Done():
			break loop
		case <-s.stop:
			break loop
		case now := <-tick:
			if s.idle(now) {
				result = ErrIdle
				break loop
			}
		}
	}
	// WebSockets are hijacked, so Shutdown does not wait for them: close the
	// terminals first, which closes their views with 1001.
	s.terms.closeAll()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.srv.Close()
	}
	return result
}

// Close stops the server immediately and closes every terminal.
func (s *Server) Close() error {
	s.terms.closeAll()
	s.lifeCancel()
	return s.srv.Close()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/exchange", s.handleExchange)
	mux.HandleFunc("POST /api/v1/auth/ticket", s.requireToken(s.handleTicket))
	mux.HandleFunc("GET /api/v1/server", s.requireToken(s.handleServer))
	mux.HandleFunc("GET /api/v1/launches/{id}", s.requireToken(s.handleLaunch))
	mux.HandleFunc("POST /api/v1/launches/{id}/terms", s.requireToken(s.handleLaunchTerms))
	mux.HandleFunc("GET /api/v1/runs", s.requireToken(s.handleRuns))
	mux.HandleFunc("GET /api/v1/runs/{lab}", s.requireToken(s.handleRun))
	mux.HandleFunc("GET /api/v1/runs/{lab}/questions/{id}", s.requireToken(s.handleQuestion))
	mux.HandleFunc("POST /api/v1/runs/{lab}/start", s.requireToken(s.handleStart))
	mux.HandleFunc("GET /api/v1/runs/{lab}/tabs/{name}/doc", s.requireToken(s.handleTabDoc))
	mux.HandleFunc("POST /api/v1/runs/{lab}/submit", s.requireToken(s.handleSubmit))
	mux.HandleFunc("GET /api/v1/runs/{lab}/submission", s.requireToken(s.handleSubmission))
	mux.HandleFunc("POST /api/v1/runs/{lab}/destroy", s.requireToken(s.handleDestroy))
	mux.HandleFunc("GET /api/v1/runs/{lab}/terminals", s.requireToken(s.handleTerminals))
	mux.HandleFunc("POST /api/v1/runs/{lab}/terminals", s.requireToken(s.handleOpenTerminal))
	mux.HandleFunc("/api/", s.requireToken(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	}))
	mux.HandleFunc("GET /ws/v1/terminals/{id}", s.handleTerminal)
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("GET /control/v1/status", s.requireControl(s.handleControlStatus))
	mux.HandleFunc("POST /control/v1/stop", s.requireControl(s.handleControlStop))
	mux.HandleFunc("/control/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, err := s.auth.exchange(req.Code)
	switch {
	case errors.Is(err, ErrCodeUsed), errors.Is(err, ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) handleTicket(w http.ResponseWriter, _ *http.Request) {
	ticket, expires, err := s.auth.newTicket()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not create a ticket")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expiresAt": expires.UTC()})
}

func (s *Server) handleServer(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   s.cfg.Version,
		"profile":   s.cfg.Profile,
		"startedAt": s.startedAt.UTC(),
		"serverNow": time.Now().UTC(),
		"features":  []string{"runs", "terminals", "tabs", "submit"},
		"assets":    s.assetsKind(),
	})
}

func (s *Server) assetsKind() string {
	if s.cfg.Assets == nil && web.IsPlaceholder() {
		return "placeholder"
	}
	return "built"
}

// handleStatic serves regular files from the frontend, index.html for "/".
// It never lists directories.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	info, err := fs.Stat(s.assets, name)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, err := fs.ReadFile(s.assets, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if name == "index.html" {
		// The page's own policy names the frames its lab tabs show.
		w.Header().Set("Content-Security-Policy", s.pagePolicy())
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	// Requiring JSON also means a cross-site form cannot reach this route
	// without a preflight, which the guard refuses.
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return errors.New("content type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes {"error": msg, "code": …} with the code that goes with
// status (see docs/reference/workspace-api.md).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeErrorCode(w, status, statusCode(status), msg)
}

// writeErrorCode writes an error with a specific code, such as not_started.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func statusCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusMisdirectedRequest:
		return "unexpected_host"
	case http.StatusNotImplemented:
		return "unsupported"
	case http.StatusServiceUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}
