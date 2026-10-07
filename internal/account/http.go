package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	// requestTimeout bounds every call to the site or the issuer.
	requestTimeout = 15 * time.Second
	// maxResponse caps how much of a response is read: every answer this
	// package expects is a small JSON document.
	maxResponse  = 1 << 20
	maxRedirects = 3
)

// newHTTPClient is the client for the site and the issuer: default TLS
// verification, a timeout, and redirects only within the same scheme and
// host (a token must never follow a redirect to somewhere else).
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if !sameOrigin(req.URL, via[0].URL) {
				return fmt.Errorf("%s redirects to %s — refusing to follow it to another site (set %s to the site's real address)",
					via[0].URL.Redacted(), req.URL.Redacted(), SiteEnv)
			}
			return nil
		},
	}
}

// httpError is a non-2xx answer: its status and the server's own message
// (sanitized), never the raw body.
type httpError struct {
	Status  int
	Code    string // OAuth "error" field, when there is one
	Message string
}

func (e *httpError) Error() string {
	msg := fmt.Sprintf("HTTP %d", e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// IsNotFound reports whether err is an HTTP 404 answer.
func IsNotFound(err error) bool {
	var he *httpError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// do sends req and decodes a 2xx JSON answer into out (when out is non-nil).
// Other statuses come back as *httpError.
func do(client *http.Client, req *http.Request, out any) (int, error) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "astrona-cli")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", req.Method, req.URL.Redacted(), stripURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: reading the answer: %w", req.Method, req.URL.Redacted(), err)
	}
	if len(body) > maxResponse {
		return resp.StatusCode, fmt.Errorf("%s %s: the answer is larger than %d bytes", req.Method, req.URL.Redacted(), maxResponse)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, errorFromBody(resp.StatusCode, body)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: the answer isn't the JSON expected: %w", req.Method, req.URL.Redacted(), err)
	}
	return resp.StatusCode, nil
}

// stripURLError drops *url.Error's own copy of the URL (already in the
// message, and a query string could carry something sensitive).
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// errorFromBody reads the message out of an error answer: OAuth
// ({error, error_description}), Nuxt/h3 ({statusMessage, message}) or
// FastAPI ({detail}).
func errorFromBody(status int, body []byte) *httpError {
	var b struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		StatusMessage    string `json:"statusMessage"`
		Message          string `json:"message"`
		Detail           any    `json:"detail"`
	}
	e := &httpError{Status: status}
	if json.Unmarshal(body, &b) != nil {
		return e
	}
	e.Code = sanitize(b.Error)
	detail, _ := b.Detail.(string)
	for _, m := range []string{b.ErrorDescription, b.StatusMessage, b.Message, detail} {
		if m = sanitize(m); m != "" {
			e.Message = m
			break
		}
	}
	return e
}

// sanitize makes server text safe to show: printable characters only (no
// terminal escapes), one line, at most 200 characters.
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	_, err = do(client, req, out)
	return err
}

func postForm(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(client, req, out)
}
