package workspace

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultCodeTTL is how long a launch code minted with ttl 0 stays valid.
	DefaultCodeTTL = 2 * time.Minute
	// MaxCodeTTL caps every launch code, whoever minted it.
	MaxCodeTTL = 5 * time.Minute
	// TicketTTL is how long a WebSocket or event-stream ticket stays valid.
	TicketTTL = 30 * time.Second

	secretLen = 32
	nonceLen  = 16
	macLen    = sha256.Size
	codeLen   = nonceLen + 8 + macLen
	tokenLen  = 32
	// maxTokens bounds the tokens one server start hands out; the oldest is
	// forgotten first. Each tab that opens a launch URL uses one.
	maxTokens = 64
	// maxTickets bounds outstanding tickets.
	maxTickets = 256

	codeMACLabel = "astrona-workspace-code-v1"
)

var (
	// ErrInvalidCode means the launch code is malformed, forged or expired.
	ErrInvalidCode = errors.New("invalid or expired launch code")
	// ErrCodeUsed means the launch code was already exchanged.
	ErrCodeUsed = errors.New("launch code already used")
)

var b64 = base64.RawURLEncoding

// NewSecret returns a fresh random secret for one server start.
func NewSecret() ([]byte, error) {
	s := make([]byte, secretLen)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("failed to create workspace secret: %w", err)
	}
	return s, nil
}

// MintCode creates a single-use launch code under secret, valid for ttl
// (DefaultCodeTTL when ttl <= 0, capped at MaxCodeTTL).
func MintCode(secret []byte, now time.Time, ttl time.Duration) (string, error) {
	if len(secret) != secretLen {
		return "", errors.New("workspace secret has the wrong length")
	}
	if ttl <= 0 {
		ttl = DefaultCodeTTL
	}
	ttl = min(ttl, MaxCodeTTL)
	buf := make([]byte, codeLen)
	if _, err := rand.Read(buf[:nonceLen]); err != nil {
		return "", fmt.Errorf("failed to create launch code: %w", err)
	}
	binary.BigEndian.PutUint64(buf[nonceLen:nonceLen+8], uint64(now.Add(ttl).Unix())) //nolint:gosec // G115: a Unix time after 1970 is positive
	copy(buf[nonceLen+8:], codeMAC(secret, buf[:nonceLen+8]))
	return b64.EncodeToString(buf), nil
}

func codeMAC(secret, payload []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(codeMACLabel))
	m.Write(payload)
	return m.Sum(nil)
}

// auth holds one server start's secret, the nonces of exchanged codes, the
// issued session tokens and the outstanding tickets. Tokens and tickets are
// stored only as SHA-256 digests.
type auth struct {
	secret []byte
	now    func() time.Time

	mu      sync.Mutex
	used    map[[nonceLen]byte]time.Time // nonce -> code expiry
	tokens  [][sha256.Size]byte          // oldest first
	tickets map[[sha256.Size]byte]time.Time
}

func newAuth(secret []byte, now func() time.Time) *auth {
	return &auth{
		secret:  secret,
		now:     now,
		used:    map[[nonceLen]byte]time.Time{},
		tickets: map[[sha256.Size]byte]time.Time{},
	}
}

// exchange verifies code and, the first time only, returns a new session
// token.
func (a *auth) exchange(code string) (string, error) {
	raw, err := b64.DecodeString(code)
	if err != nil || len(raw) != codeLen {
		return "", ErrInvalidCode
	}
	if !hmac.Equal(raw[nonceLen+8:], codeMAC(a.secret, raw[:nonceLen+8])) {
		return "", ErrInvalidCode
	}
	now := a.now()
	expires := time.Unix(int64(binary.BigEndian.Uint64(raw[nonceLen:nonceLen+8])), 0) //nolint:gosec // G115: authenticated by the MAC above
	// A code that claims to live longer than MaxCodeTTL was not minted by
	// MintCode; refuse it rather than remember its nonce for that long.
	if !now.Before(expires) || expires.Sub(now) > MaxCodeTTL+time.Second {
		return "", ErrInvalidCode
	}
	var nonce [nonceLen]byte
	copy(nonce[:], raw[:nonceLen])

	token, digest, err := randomSecret()
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for n, exp := range a.used {
		if !now.Before(exp) {
			delete(a.used, n)
		}
	}
	if _, seen := a.used[nonce]; seen {
		return "", ErrCodeUsed
	}
	a.used[nonce] = expires
	a.tokens = append(a.tokens, digest)
	if len(a.tokens) > maxTokens {
		a.tokens = a.tokens[len(a.tokens)-maxTokens:]
	}
	return token, nil
}

// validToken reports whether token was issued by this server start. Every
// stored digest is compared, in constant time, so the time taken does not
// depend on which token matched or how much of it.
func (a *auth) validToken(token string) bool {
	if token == "" {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	a.mu.Lock()
	defer a.mu.Unlock()
	match := 0
	for i := range a.tokens {
		match |= subtle.ConstantTimeCompare(digest[:], a.tokens[i][:])
	}
	return match == 1
}

// newTicket returns a single-use ticket valid for TicketTTL.
func (a *auth) newTicket() (string, time.Time, error) {
	ticket, digest, err := randomSecret()
	if err != nil {
		return "", time.Time{}, err
	}
	now := a.now()
	expires := now.Add(TicketTTL)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneTicketsLocked(now)
	if len(a.tickets) >= maxTickets {
		return "", time.Time{}, errors.New("too many outstanding tickets")
	}
	a.tickets[digest] = expires
	return ticket, expires, nil
}

// useTicket consumes ticket. It succeeds once, before the ticket expires.
// The lookup is keyed by the ticket's SHA-256 digest, so its timing reveals
// nothing an attacker could use to guess the ticket itself.
func (a *auth) useTicket(ticket string) bool {
	if ticket == "" {
		return false
	}
	digest := sha256.Sum256([]byte(ticket))
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.tickets[digest]
	delete(a.tickets, digest)
	a.pruneTicketsLocked(now)
	return ok && now.Before(exp)
}

func (a *auth) pruneTicketsLocked(now time.Time) {
	for d, exp := range a.tickets {
		if !now.Before(exp) {
			delete(a.tickets, d)
		}
	}
}

func randomSecret() (string, [sha256.Size]byte, error) {
	b := make([]byte, tokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("failed to create token: %w", err)
	}
	s := b64.EncodeToString(b)
	return s, sha256.Sum256([]byte(s)), nil
}
