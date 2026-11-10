package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"astrona/internal/account"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var (
	// ErrRevoked: the website signed this computer out. Its sign-in is
	// removed; `astrona login` signs it in again.
	ErrRevoked = errors.New("this computer was signed out from the website")
	// ErrReplaced: another `astrona connect` for this computer took over.
	ErrReplaced = errors.New("another astrona connect on this computer took over the connection")
	// ErrRejected marks a Runner error as a refusal (the command was not
	// run at all) rather than a failure.
	ErrRejected = errors.New("refused")
)

// Runner runs one command; nil is success. It is called for one command at
// a time, never two at once.
type Runner func(ctx context.Context, c Command) error

// Options configure Run.
type Options struct {
	Client *account.Client
	Store  account.Store
	Site   string
	// Hello returns this computer's facts for a new connection (version,
	// OS, engine, capacity). Type, Running and LastDisconnect are set here.
	Hello func(ctx context.Context) Hello
	Run   Runner
	// Log gets one line per event a person following the connection cares
	// about (connected, a command, a drop and the next try).
	Log io.Writer
}

const (
	dialTimeout      = 20 * time.Second
	welcomeTimeout   = 15 * time.Second
	defaultHeartbeat = 25 * time.Second
	maxBackoff       = 60 * time.Second
	// stableAfter: a connection that lasted this long resets the backoff.
	stableAfter = 30 * time.Second
	readLimit   = 64 << 10
	queueSize   = 8
)

// URL is the socket's address on site: /api/cli/connect, over wss (ws for
// a local http site).
func URL(site string) (string, error) {
	u, err := url.Parse(site)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported site URL %s", site)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/cli/connect"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

type client struct {
	o       Options
	results chan result
	work    chan Command
	mu      sync.Mutex
	running *Running
}

// Run keeps this computer connected until ctx is done (nil), the site
// revokes it (ErrRevoked) or replaces it (ErrReplaced), or the sign-in is
// gone (wrapping account.ErrSignedOut). Drops reconnect with backoff, 1 s
// doubling to 60 s with jitter.
func Run(ctx context.Context, o Options) error {
	c := &client{o: o, results: make(chan result, 64), work: make(chan Command, queueSize)}
	go c.worker(ctx)
	var last *Disconnect
	attempts := 0
	for {
		started := time.Now()
		d, err := c.connectOnce(ctx, last, attempts)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Since(started) >= stableAfter {
			attempts = 0
		}
		attempts++
		d.Attempts = attempts
		last = d
		wait := backoff(attempts)
		if d.Kind == "unauthorized" && attempts == 1 {
			wait = 0 // the sign-in was renewed: try again at once
		}
		c.logf("Disconnected (%s). Reconnecting in %s.", describe(d), wait.Round(time.Second))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// backoff is the wait before attempt n (1-based): 2^(n-1) s capped at
// 60 s, with the upper half jittered so many computers don't return at once.
func backoff(n int) time.Duration {
	d := maxBackoff
	if n < 7 {
		d = min(time.Duration(1<<(n-1))*time.Second, maxBackoff)
	}
	return d/2 + rand.N(d/2+1) //nolint:gosec // jitter, not a secret
}

func describe(d *Disconnect) string {
	s := d.Kind
	if d.CloseCode != 0 {
		s += fmt.Sprintf(", code %d", d.CloseCode)
	}
	if d.Error != "" {
		s += ": " + d.Error
	}
	return s
}

func (c *client) logf(format string, args ...any) {
	if c.o.Log != nil {
		fmt.Fprintf(c.o.Log, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}
}

// connectOnce holds one connection until it ends. It returns how it ended,
// or an error when Run must stop.
func (c *client) connectOnce(ctx context.Context, last *Disconnect, attempts int) (*Disconnect, error) {
	lost := func(kind string, err error, code int) *Disconnect {
		d := &Disconnect{At: time.Now().UTC(), Kind: kind, CloseCode: code}
		if err != nil {
			d.Error = clean(err.Error(), 240)
		}
		return d
	}
	creds, err := c.o.Client.Active(ctx, c.o.Store, c.o.Site)
	if errors.Is(err, account.ErrSignedOut) {
		return nil, err
	}
	if err != nil {
		return lost("network", err, 0), nil
	}
	target, err := URL(creds.Site)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	ws, resp, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + creds.AccessToken}},
	})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return c.unauthorized(ctx, creds, lost("unauthorized", nil, 0))
		}
		return lost("network", err, 0), nil
	}
	defer ws.CloseNow()
	ws.SetReadLimit(readLimit)

	hello := c.o.Hello(ctx)
	hello.Type = "hello"
	c.mu.Lock()
	hello.Running = c.running
	c.mu.Unlock()
	if last != nil {
		l := *last
		l.Attempts = attempts
		l.OfflineSeconds = time.Since(l.At).Seconds()
		hello.LastDisconnect = &l
	}
	if err := writeJSON(ctx, ws, hello); err != nil {
		return c.ended(ctx, creds, ws, err, lost)
	}
	welcome, err := readFrame(ctx, ws, welcomeTimeout)
	if err != nil {
		return c.ended(ctx, creds, ws, err, lost)
	}
	if welcome.Type != "welcome" {
		ws.Close(CloseProtocolError, "expected welcome")
		return lost("protocol", fmt.Errorf("expected welcome, got %q", clean(welcome.Type, 32)), CloseProtocolError), nil
	}
	heartbeat := defaultHeartbeat
	if welcome.HeartbeatSeconds > 0 && welcome.HeartbeatSeconds <= 600 {
		heartbeat = time.Duration(welcome.HeartbeatSeconds) * time.Second
	}
	who := creds.DeviceName
	if who == "" {
		who = "this computer"
	}
	c.logf("Connected to %s as %s. The website can start labs here; Ctrl+C disconnects.", creds.Site, who)

	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	go c.sendResults(connCtx, ws)

	for {
		// Pings come every heartbeat: three missed ones mean the link is gone.
		f, err := readFrame(connCtx, ws, 3*heartbeat+5*time.Second)
		if err != nil {
			return c.ended(ctx, creds, ws, err, lost)
		}
		switch f.Type {
		case "ping":
			if err := writeJSON(connCtx, ws, map[string]any{"type": "pong", "id": f.ID}); err != nil {
				return c.ended(ctx, creds, ws, err, lost)
			}
		case "command":
			c.command(connCtx, ws, f)
		}
		// Unknown frame types are ignored: a newer site may send more.
	}
}

// ended turns why the connection ended into a Disconnect, or into the error
// that stops Run (revoked, replaced, signed out).
func (c *client) ended(ctx context.Context, creds *account.Credentials, ws *websocket.Conn, err error, lost func(string, error, int) *Disconnect) (*Disconnect, error) {
	if ctx.Err() != nil {
		ws.Close(websocket.StatusGoingAway, "astrona connect stopped")
		return nil, nil
	}
	code := int(websocket.CloseStatus(err))
	switch code {
	case CloseRevoked:
		if _, derr := c.o.Store.DeleteIfCurrent(ctx, creds); derr != nil {
			c.logf("Could not remove the saved sign-in: %v", derr)
		}
		return nil, ErrRevoked
	case CloseReplaced:
		return nil, ErrReplaced
	case CloseUnauthorized:
		return c.unauthorized(ctx, creds, lost("unauthorized", nil, code))
	case CloseHeartbeatTimeout:
		return lost("heartbeat", nil, code), nil
	case CloseProtocolError, CloseHelloTimeout:
		return lost("protocol", nil, code), nil
	case -1:
		if errors.Is(err, context.DeadlineExceeded) {
			ws.Close(websocket.StatusGoingAway, "no ping from the site")
			return lost("heartbeat", errors.New("no ping from the site"), 0), nil
		}
		return lost("network", err, 0), nil
	default:
		return lost("server_closed", nil, code), nil
	}
}

// unauthorized renews the sign-in after the site rejected the access
// token; a refused renewal stops Run.
func (c *client) unauthorized(ctx context.Context, creds *account.Credentials, d *Disconnect) (*Disconnect, error) {
	if _, err := c.o.Client.Renew(ctx, c.o.Store, creds); err != nil {
		if errors.Is(err, account.ErrSignedOut) {
			return nil, err
		}
		d.Error = clean(err.Error(), 240)
	}
	return d, nil
}

// command acknowledges a valid command and queues it, or refuses it.
func (c *client) command(ctx context.Context, ws *websocket.Conn, f frame) {
	var id string
	_ = json.Unmarshal(f.ID, &id)
	cmd := Command{ID: id, Command: f.Command, Lab: f.Lab, Session: f.Session}
	if !idPattern.MatchString(id) {
		return // nothing to answer to
	}
	if !cmd.Valid() {
		c.logf("Refused a request from the website that is not an allowed lab command.")
		c.results <- result{Type: "result", ID: id, Outcome: "rejected", Error: "not an allowed lab command"}
		return
	}
	if err := writeJSON(ctx, ws, map[string]string{"type": "ack", "id": id}); err != nil {
		return // the read loop sees the drop; the site times the command out
	}
	select {
	case c.work <- cmd:
	default:
		c.results <- result{Type: "result", ID: id, Outcome: "rejected", Error: "this computer is busy with other lab commands"}
	}
}

// worker runs queued commands one at a time; their results wait for a
// connection if there is none.
func (c *client) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-c.work:
			c.logf("The website asked to %s %s.", cmd.Command, cmd.Lab)
			err := c.o.Run(ctx, cmd)
			r := result{Type: "result", ID: cmd.ID, Outcome: "ok"}
			switch {
			case errors.Is(err, ErrRejected):
				r.Outcome, r.Error = "rejected", clean(err.Error(), 240)
			case err != nil:
				r.Outcome, r.Error = "failed", clean(err.Error(), 240)
			}
			c.logf("%s %s: %s", cmd.Command, cmd.Lab, describeResult(r))
			if r.Outcome == "ok" {
				c.track(cmd)
			}
			select {
			case c.results <- r:
			default: // a long outage with many results: the oldest are on the site as timed out
			}
		}
	}
}

func describeResult(r result) string {
	if r.Error == "" {
		return r.Outcome
	}
	return r.Outcome + " (" + r.Error + ")"
}

// track remembers the lab run for the website, for the next hello.
func (c *client) track(cmd Command) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case cmd.Command == "run" && cmd.Session != nil:
		c.running = &Running{Lab: cmd.Lab, SessionID: cmd.Session.ID}
	case cmd.Command == "destroy" && c.running != nil && c.running.Lab == cmd.Lab:
		c.running = nil
	}
}

// sendResults writes finished commands' results while the connection lasts.
func (c *client) sendResults(ctx context.Context, ws *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-c.results:
			if err := writeJSON(ctx, ws, r); err != nil {
				select {
				case c.results <- r: // send it on the next connection
				default:
				}
				return
			}
		}
	}
}

func writeJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(wctx, ws, v)
}

func readFrame(ctx context.Context, ws *websocket.Conn, timeout time.Duration) (frame, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var f frame
	typ, data, err := ws.Read(rctx)
	if err != nil {
		if rctx.Err() != nil && ctx.Err() == nil {
			return f, context.DeadlineExceeded
		}
		return f, err
	}
	if typ != websocket.MessageText || json.Unmarshal(data, &f) != nil {
		return frame{}, nil // not a frame we know: ignored like an unknown type
	}
	return f, nil
}

// clean makes s one printable line of at most n runes.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
