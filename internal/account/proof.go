package account

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"astrona/internal/transport"
)

// ProofError is the site refusing a call's device-key proof (401
// proof_required or proof_invalid): this computer's key does not match its
// sign-in, or its clock is far off. Renewing the sign-in would not help, so
// it is never retried that way; `astrona login` makes a new key.
type ProofError struct {
	Site    string
	Code    string
	Message string
}

func (e *ProofError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "This computer's key does not match its sign-in. Run `astrona login`."
	}
	return fmt.Sprintf("%s refused this computer's key (%s): %s", e.Site, e.Code, msg)
}

// IsProofError reports whether err is the site refusing this computer's
// device-key proof.
func IsProofError(err error) bool {
	var pe *ProofError
	return errors.As(err, &pe)
}

const (
	// keyEnrolBackoff is how long a sign-in waits before trying to enrol a
	// key again after the site refused it or does not offer it.
	keyEnrolBackoff = 24 * time.Hour
	// skewIgnored is clock skew too small to correct: the site allows
	// LABS_TRANSPORT_PROOF_WINDOW_SECONDS (120 s by default).
	skewIgnored = 10
	// skewRetry is how much the estimate must move before a refused proof
	// is sent again with the corrected time.
	skewRetry = 30
)

// send sends req with cr's access token and, when cr has its device key,
// the Astrona-Proof header.
func (c *Client) send(req *http.Request, cr *Credentials, out any) (int, error) {
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	return c.signed(req, cr.key, cr.AccessToken, out, maxResponse)
}

// signed sends req (its Authorization header already set) with a proof by
// key over token and the exact body, when key is not nil. A proof the site
// refuses comes back as *ProofError; when the refusal came with a Date far
// from this computer's clock, the call is sent once more timed by the
// site's clock.
func (c *Client) signed(req *http.Request, key *transport.Key, token string, out any, limit int64) (int, error) {
	for attempt := 0; ; attempt++ {
		used := c.skew.Load()
		if key != nil {
			body, err := requestBody(req)
			if err != nil {
				return 0, err
			}
			proof, err := key.RequestProof(req.Method, req.URL.Path, c.Now().Unix()+used, token, body)
			if err != nil {
				return 0, err
			}
			req.Header.Set(transport.Header, proof)
		}
		status, header, err := doHeaders(c.HTTP, req, out, limit)
		moved := c.observeClock(header, used)
		var he *httpError
		if !errors.As(err, &he) || !strings.HasPrefix(he.Code, "proof_") {
			return status, err
		}
		if attempt == 0 && key != nil && moved && req.GetBody != nil {
			if req.Body, err = req.GetBody(); err == nil {
				continue
			}
		}
		return status, &ProofError{Site: req.URL.Scheme + "://" + req.URL.Host, Code: he.Code, Message: he.Message}
	}
}

// requestBody is a copy of req's body, empty for a call without one.
func requestBody(req *http.Request) ([]byte, error) {
	if req.GetBody == nil {
		return nil, nil
	}
	rc, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxResponse))
}

// observeClock updates the site's clock skew from an answer's Date header.
// It reports whether the estimate moved far enough from used to make a
// refused proof worth sending again.
func (c *Client) observeClock(header http.Header, used int64) bool {
	if header == nil {
		return false
	}
	date, err := http.ParseTime(header.Get("Date"))
	if err != nil {
		return false
	}
	skew := date.Unix() - c.Now().Unix()
	if abs(skew) < skewIgnored {
		skew = 0
	}
	c.skew.Store(skew)
	return abs(skew-used) >= skewRetry
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// keyEnrolment is POST {site}/api/cli/device/key.
type keyEnrolment struct {
	RefreshToken string `json:"refresh_token"`
	transport.Registration
}

// enrolKey gives a sign-in from before device keys its key, right after a
// renewal (so the site is known to be reachable). The key is saved before
// the call; the site rotates the refresh token, and the answer is saved
// with the key's id. A site that refuses or lacks enrolment is asked again
// after keyEnrolBackoff. Nothing here fails the command, except losing the
// rotated sign-in, as Renew would.
func (c *Client) enrolKey(ctx context.Context, s Store, cr *Credentials) (*Credentials, error) {
	if cr.KeyID != "" || cr.DeviceID == "" || c.Now().Before(cr.KeyEnrolAfter) {
		return cr, nil
	}
	key, err := transport.Generate()
	if err != nil {
		return cr, nil
	}
	kf := s.KeyFile()
	if err := kf.Add(key, c.Now()); err != nil {
		return cr, nil
	}
	body := keyEnrolment{
		RefreshToken: cr.RefreshToken,
		Registration: key.Register(transport.Hash([]byte(cr.RefreshToken))),
	}
	req, err := jsonRequest(ctx, http.MethodPost, cr.Site+"/api/cli/device/key", body)
	if err != nil {
		return cr, nil
	}
	var t Tokens
	_, err = do(c.HTTP, req, &t)
	if err == nil {
		err = t.check()
	}
	next := *cr
	switch {
	case err == nil:
		next.apply(t, c.Now())
		if t.KeyID == key.ID {
			next.KeyID, next.key = key.ID, key
		}
	case Answered(err):
		// Refused (an old site, a device that already has a key): the
		// refresh token did not rotate. Ask again later.
		_ = kf.Keep("")
		next.KeyEnrolAfter = c.Now().Add(keyEnrolBackoff)
	default:
		// Not reached; try at the next renewal.
		return cr, nil
	}
	if err := s.Save(&next); err != nil {
		if next.RefreshToken == cr.RefreshToken {
			return cr, nil // only the backoff was lost
		}
		return nil, fmt.Errorf("your sign-in was renewed but couldn't be saved, so it is lost — run `astrona login` again: %w", err)
	}
	_ = kf.Keep(next.KeyID)
	return &next, nil
}
