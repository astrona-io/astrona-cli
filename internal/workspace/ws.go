package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"astrona/internal/termbridge"
)

const (
	maxInputFrame = 64 << 10
	outputChunk   = 32 << 10

	closeTakenOver websocket.StatusCode = 4001
	closeOverrun   websocket.StatusCode = 4002

	strictRecheck = 5 * time.Second
)

// strictDeadline is the time limit of lab's started strict exam, or zero
// when the run is not strict or has no running clock.
func strictDeadline(lab string) time.Time {
	rec, err := ReadRun(lab)
	if err != nil || !rec.Strict {
		return time.Time{}
	}
	v, err := buildView(rec, time.Now())
	if err != nil || v.DeadlineAt == nil {
		return time.Time{}
	}
	return *v.DeadlineAt
}

type controlMessage struct {
	Type     string        `json:"type"`
	Cols     int           `json:"cols,omitempty"`
	Rows     int           `json:"rows,omitempty"`
	ExitCode *int          `json:"exitCode,omitempty"`
	Message  string        `json:"message,omitempty"`
	Terminal *TerminalInfo `json:"terminal,omitempty"`
}

// handleTerminal attaches a WebSocket to a terminal. The guard has already
// required an allowed Origin for the upgrade; the ticket proves the token.
// Everything that can fail is checked before the upgrade, so the page gets a
// normal HTTP error.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !isUpgrade(r) {
		writeError(w, http.StatusBadRequest, "websocket upgrade required")
		return
	}
	if !s.auth.useTicket(r.URL.Query().Get("ticket")) {
		writeError(w, http.StatusUnauthorized, "missing or invalid ticket")
		return
	}
	t := s.terms.get(r.PathValue("id"))
	if t == nil {
		writeError(w, http.StatusNotFound, "no such terminal")
		return
	}
	release := s.Hold()
	defer release()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{fmt.Sprintf("%s:%d", loopbackAddr, s.port), fmt.Sprintf("localhost:%d", s.port)},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return // Accept has written the error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxInputFrame)
	s.bridge(conn, t)
}

func (s *Server) bridge(conn *websocket.Conn, t *terminal) {
	ctx, cancel := context.WithCancel(s.life)
	defer cancel()
	send := func(msg controlMessage) error {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		return conn.Write(ctx, websocket.MessageText, data)
	}

	client, replay := t.session.Attach()
	defer client.Close()
	view := t.view()
	if send(controlMessage{Type: "attached", Terminal: &view}) != nil {
		return
	}
	for len(replay) > 0 {
		n := min(len(replay), outputChunk)
		if conn.Write(ctx, websocket.MessageBinary, replay[:n]) != nil {
			return
		}
		replay = replay[n:]
	}

	// Input: binary frames are keystrokes, text frames are control JSON.
	go func() {
		// Detaching unblocks client.Read in the output loop below.
		defer client.Close()
		defer cancel()
		// A strict exam's terminal is read-only once its time is up. The
		// deadline is looked up at attach and, while there is none yet,
		// again at most every few seconds rather than on every keystroke.
		deadline, checked := strictDeadline(t.lab), time.Now()
		warned := false
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if deadline.IsZero() && time.Since(checked) > strictRecheck {
					deadline, checked = strictDeadline(t.lab), time.Now()
				}
				if !deadline.IsZero() && !time.Now().Before(deadline) {
					if !warned {
						warned = true
						_ = send(controlMessage{Type: "error", Message: "Time is up: in this strict exam the terminal is read-only. Submit to record your result."})
					}
					continue
				}
				_, _ = client.Write(data) // a dead client ends the output loop
				continue
			}
			var msg controlMessage
			if json.Unmarshal(data, &msg) != nil {
				conn.Close(websocket.StatusPolicyViolation, "invalid control message")
				return
			}
			switch msg.Type {
			case "ping":
				_ = send(controlMessage{Type: "pong"})
			case "resize":
				if msg.Cols < 1 || msg.Rows < 1 {
					_ = send(controlMessage{Type: "error", Message: "invalid size"})
				} else if err := client.Resize(msg.Cols, msg.Rows); err != nil {
					_ = send(controlMessage{Type: "error", Message: err.Error()})
				}
			default:
				_ = send(controlMessage{Type: "error", Message: "unknown message type"})
			}
		}
	}()

	buf := make([]byte, outputChunk)
	for {
		n, err := client.Read(buf)
		if n > 0 && conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
			return
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF):
			if t.isGone() {
				conn.Close(websocket.StatusGoingAway, "terminal closed")
				return
			}
			code := t.exitCode()
			_ = send(controlMessage{Type: "ended", ExitCode: &code})
			conn.Close(websocket.StatusNormalClosure, "ended")
		case errors.Is(err, termbridge.ErrTakenOver):
			_ = send(controlMessage{Type: "takenOver"})
			conn.Close(closeTakenOver, "taken over")
		case errors.Is(err, termbridge.ErrOverrun):
			_ = send(controlMessage{Type: "overrun"})
			conn.Close(closeOverrun, "overrun")
		}
		// ErrDetached: the WebSocket went away first.
		return
	}
}
