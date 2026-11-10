// Package connect is the client side of `astrona connect`: one outbound
// WebSocket from this computer to the Astrona site, kept open, over which
// the website starts catalog labs on it ("Start on my machine").
//
// The protocol is JSON text frames:
//
//	CLI → site   hello (first frame), pong, ack, result
//	site → CLI   challenge (first, to a computer with a device key),
//	             welcome, ping, command
//
// A computer with a device key waits for the challenge and signs it in its
// hello's proof (account.Credentials.ConnectProof); the site checks it
// against the key it registered and closes with 4007 when it does not match.
//
// Only the fixed Commands travel down the socket, each naming a catalog lab.
// The package checks both before anything runs and never accepts a shell
// command, a URL or a path. Running a command is the caller's Runner; this
// package never builds a lab itself.
package connect

import (
	"encoding/json"
	"regexp"
	"time"
)

// Close codes the site uses (4000-4999 are Astrona's).
const (
	CloseReplaced         = 4000 // this device connected again elsewhere: do not reconnect
	CloseUnauthorized     = 4001 // the access token was rejected: renew, then reconnect
	CloseHeartbeatTimeout = 4002
	CloseRevoked          = 4003 // signed out from the website: drop the sign-in, do not reconnect
	CloseProtocolError    = 4004
	CloseHelloTimeout     = 4005
	// CloseTransportRequired: the site no longer accepts a computer without
	// a device key. Stop; the student updates astrona and signs in again.
	CloseTransportRequired = 4006
	// CloseHandshakeFailed: the hello's proof does not match this computer's
	// key. Stop; it does not heal on its own, `astrona login` fixes it.
	CloseHandshakeFailed = 4007
)

// Commands is everything the site may ask for, each for one catalog lab.
// renew is for a running playground only; for any other lab it fails with
// renew's own error.
var Commands = []string{"run", "status", "submit", "reset", "destroy", "stop", "start", "renew"}

// labPattern is a catalog lab name (TRAINING/section-010/lab-01), the same
// shape the site accepts. No dots-only segments, no leading dash or slash.
var labPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9][A-Za-z0-9._-]{0,63}){1,6}$`)

// idPattern is a command or session id (a UUID).
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Capacity is what this computer can give a lab.
type Capacity struct {
	CPUs          int   `json:"cpus,omitempty"`
	MemoryBytes   int64 `json:"memory_bytes,omitempty"`
	DiskFreeBytes int64 `json:"disk_free_bytes,omitempty"`
}

// Running is the lab this computer runs for the website, if any.
type Running struct {
	Lab       string `json:"lab"`
	SessionID string `json:"session_id,omitempty"`
}

// Disconnect is how the previous connection ended, as this CLI saw it. The
// site keeps it with the next connection, for support.
type Disconnect struct {
	At             time.Time `json:"at"`
	Kind           string    `json:"kind"` // network, server_closed, heartbeat, unauthorized, protocol
	Error          string    `json:"error,omitempty"`
	CloseCode      int       `json:"close_code,omitempty"`
	Attempts       int       `json:"attempts"`
	OfflineSeconds float64   `json:"offline_seconds"`
}

// Hello is the first frame on every connection.
type Hello struct {
	Type           string      `json:"type"`
	CLIVersion     string      `json:"cli_version,omitempty"`
	OS             string      `json:"os,omitempty"`
	Arch           string      `json:"arch,omitempty"`
	Engine         string      `json:"engine,omitempty"`
	Capacity       *Capacity   `json:"capacity,omitempty"`
	Running        *Running    `json:"running,omitempty"`
	LastDisconnect *Disconnect `json:"last_disconnect,omitempty"`
	// Proof answers the site's challenge, from a computer with a device key.
	Proof string `json:"proof,omitempty"`
}

// SessionRef names the lab session a `run` is for. The CLI takes it over
// through the site's API; its token never travels down the socket.
type SessionRef struct {
	ID   string `json:"id"`
	Lab  string `json:"lab"`
	Kind string `json:"kind"`
}

// Command is one request from the website.
type Command struct {
	ID      string      `json:"id"`
	Command string      `json:"command"`
	Lab     string      `json:"lab"`
	Session *SessionRef `json:"session,omitempty"`
}

// frame is any frame from the site; Type picks which fields mean something.
type frame struct {
	Type             string          `json:"type"`
	ID               json.RawMessage `json:"id"`
	Command          string          `json:"command"`
	Lab              string          `json:"lab"`
	Session          *SessionRef     `json:"session"`
	ConnectionID     string          `json:"connection_id"`
	Nonce            string          `json:"nonce"`
	DeviceID         string          `json:"device_id"`
	HeartbeatSeconds int             `json:"heartbeat_seconds"`
}

// result is the CLI's answer once a command has ended.
type result struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Outcome string `json:"outcome"` // ok, failed, rejected
	Error   string `json:"error,omitempty"`
}

// Valid reports whether c is a command this CLI runs: one of Commands for
// a catalog lab, and `run` only with the session it is for.
func (c Command) Valid() bool {
	if !idPattern.MatchString(c.ID) || !labPattern.MatchString(c.Lab) {
		return false
	}
	known := false
	for _, name := range Commands {
		if c.Command == name {
			known = true
		}
	}
	if !known {
		return false
	}
	if c.Session != nil && (!idPattern.MatchString(c.Session.ID) || c.Session.Lab != c.Lab) {
		return false
	}
	return c.Command != "run" || c.Session != nil
}
