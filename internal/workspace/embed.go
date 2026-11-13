package workspace

import (
	"astrona/internal/telemetry"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"astrona/internal/config"
)

// A documentation link opens as a tab of the workspace, in a frame. Many
// documentation sites refuse to be framed (X-Frame-Options or CSP
// frame-ancestors), and a cross-origin frame cannot tell the page whether
// that happened: it just stays blank. So before the page frames a link it
// asks GET /runs/{lab}/doclinks/frame, which requests the link once and
// reads those two headers. Only the run's own documentation links are
// checked, never a URL the page makes up, and nothing of the response but
// its headers is used: the page still loads the site itself, in the frame.

// Frame answers of GET /runs/{lab}/doclinks/frame.
const (
	FrameAllowed = "allowed" // no header refuses it: the page frames it
	FrameRefused = "refused" // the site refuses framing: the page says so
	FrameUnknown = "unknown" // the check failed (offline, timeout): the page tries the frame
)

// frameCheckTTL is how long one answer for a URL is reused.
const frameCheckTTL = time.Hour

// frameCheckClient requests documentation links: https only, a few
// redirects, a short timeout. It follows the user's proxy settings like
// the browser would.
var frameCheckClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("redirect away from https")
		}
		return nil
	},
}

type frameAnswer struct {
	Frame  string `json:"frame"`
	Reason string `json:"reason,omitempty"`
	at     time.Time
}

// frameChecks caches answers per URL for the server's lifetime.
type frameChecks struct {
	mu      sync.Mutex
	answers map[string]frameAnswer
}

func (c *frameChecks) get(url string, now time.Time) (frameAnswer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.answers[url]
	if !ok || now.Sub(a.at) > frameCheckTTL {
		return frameAnswer{}, false
	}
	return a, true
}

func (c *frameChecks) put(url string, a frameAnswer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.answers == nil {
		c.answers = map[string]frameAnswer{}
	}
	c.answers[url] = a
}

// docLinkOrigins are the origins of rec's documentation links, for the
// page's frame-src. None in an exam.
func docLinkOrigins(rec RunRecord) []string {
	if !rec.docLinksAllowed() {
		return nil
	}
	var out []string
	for _, l := range rec.DocLinks {
		if o, err := config.TabDocsOrigin(l.URL); err == nil {
			out = append(out, o)
		}
	}
	return out
}

// handleDocLinkFrame is GET /runs/{lab}/doclinks/frame?url=<url>: whether
// the documentation link url of the run can be shown in a frame. url must be
// one of the run's docLinks; anything else is 404.
func (s *Server) handleDocLinkFrame(w http.ResponseWriter, r *http.Request) {
	rec, ok := loadRun(w, r.PathValue("lab"))
	if !ok {
		return
	}
	raw := r.URL.Query().Get("url")
	listed, index := false, 0
	if rec.docLinksAllowed() {
		for i, l := range rec.DocLinks {
			if l.URL == raw {
				listed, index = true, i
				break
			}
		}
	}
	origin, err := config.TabDocsOrigin(raw)
	if !listed || err != nil {
		writeError(w, http.StatusNotFound, "no such documentation link")
		return
	}
	// Only the link's position in the lab's list is recorded, never its URL.
	if index < 100 {
		telemetry.RecordTab(rec.Lab, telemetry.TabOpened, "doc_link", "", telemetry.Int(index))
	}
	now := time.Now()
	a, ok := s.frames.get(raw, now)
	if !ok {
		a = checkFrame(r.Context(), raw, origin)
		a.at = now
		if a.Frame != FrameUnknown {
			s.frames.put(raw, a)
		}
	}
	writeJSON(w, http.StatusOK, a)
}

// checkFrame requests raw and reads whether its final response lets
// another site frame it.
func checkFrame(ctx context.Context, raw, origin string) frameAnswer {
	// raw is one of the run's own documentation links (https, from the
	// lab's manifest), checked by the caller; never a URL the page chose.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil) //nolint:gosec // G704: see above

	if err != nil {
		return frameAnswer{Frame: FrameUnknown}
	}
	req.Header.Set("Accept", "text/html")
	resp, err := frameCheckClient.Do(req) //nolint:gosec // G704: raw is a listed documentation link
	if err != nil {
		return frameAnswer{Frame: FrameUnknown}
	}
	_ = resp.Body.Close()
	final := "https://" + resp.Request.URL.Host
	if final != origin {
		// The page's frame-src names only the link's own origin.
		return frameAnswer{Frame: FrameRefused, Reason: "it redirects to another site"}
	}
	return frameVerdict(resp.Header)
}

// frameVerdict decides from a response's headers whether a page on
// another origin may frame it. CSP frame-ancestors wins over
// X-Frame-Options, as in browsers; only "*" lets this page frame it, since
// a site never lists a loopback origin.
func frameVerdict(h http.Header) frameAnswer {
	for _, policy := range h.Values("Content-Security-Policy") {
		for _, directive := range strings.Split(policy, ";") {
			fields := strings.Fields(directive)
			if len(fields) == 0 || !strings.EqualFold(fields[0], "frame-ancestors") {
				continue
			}
			for _, src := range fields[1:] {
				if src == "*" {
					return frameAnswer{Frame: FrameAllowed}
				}
			}
			return frameAnswer{Frame: FrameRefused, Reason: "its Content-Security-Policy (frame-ancestors) refuses it"}
		}
	}
	for _, v := range h.Values("X-Frame-Options") {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "DENY", "SAMEORIGIN":
			return frameAnswer{Frame: FrameRefused, Reason: "its X-Frame-Options header refuses it"}
		}
	}
	return frameAnswer{Frame: FrameAllowed}
}
