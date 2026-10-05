package proctor

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"astrona/internal/config"
)

// matchOutput applies every matcher a check sets (expect = exact,
// contains, expectRegex) to out. No matcher set means "anything". why
// explains the first mismatch.
func matchOutput(c config.ValidationCheck, out string) (ok bool, why string) {
	if c.Expect != "" && out != c.Expect {
		return false, fmt.Sprintf("want exactly %q", c.Expect)
	}
	if c.Contains != "" && !strings.Contains(out, c.Contains) {
		return false, fmt.Sprintf("want it to contain %q", c.Contains)
	}
	if c.ExpectRegex != "" {
		re, err := regexp.Compile(c.ExpectRegex)
		if err != nil {
			return false, fmt.Sprintf("invalid expectRegex: %s", err)
		}
		if !re.MatchString(out) {
			return false, fmt.Sprintf("want it to match /%s/", c.ExpectRegex)
		}
	}
	return true, ""
}

func joinMessage(out, why string) string {
	switch {
	case why == "":
		return out
	case out == "":
		return "(no output) — " + why
	default:
		return out + " — " + why
	}
}

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func withinBounds(n int, lo, hi *int) bool {
	return (lo == nil || n >= *lo) && (hi == nil || n <= *hi)
}

func describeBounds(lo, hi *int) string {
	switch {
	case lo != nil && hi != nil && *lo == *hi:
		return "exactly " + strconv.Itoa(*lo)
	case lo != nil && hi != nil:
		return fmt.Sprintf("%d–%d", *lo, *hi)
	case lo != nil:
		return "at least " + strconv.Itoa(*lo)
	default:
		return "at most " + strconv.Itoa(*hi)
	}
}

const (
	httpCheckTimeout = 10 * time.Second
	// maxHTTPCheckBody bounds how much of a response is read for body
	// matchers.
	maxHTTPCheckBody = 1 << 20
)

// httpClient is swappable in tests.
var httpClient = &http.Client{Timeout: httpCheckTimeout}

// httpCheck GETs c.URL and checks the status (default 200) and, if any
// matcher is set, the body. Scheme/host were validated at config load
// (http/https only).
func httpCheck(c config.ValidationCheck) (bool, string) {
	want := c.ExpectStatus
	if want == 0 {
		want = http.StatusOK
	}
	resp, err := httpClient.Get(c.URL)
	if err != nil {
		return false, fmt.Sprintf("GET %s failed: %s", c.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPCheckBody))
	if err != nil {
		return false, fmt.Sprintf("reading response from %s failed: %s", c.URL, err)
	}

	if resp.StatusCode != want {
		return false, fmt.Sprintf("GET %s → %d, want %d", c.URL, resp.StatusCode, want)
	}
	if ok, why := matchOutput(c, strings.TrimSpace(string(body))); !ok {
		return false, fmt.Sprintf("GET %s → %d, but the body doesn't match: %s", c.URL, resp.StatusCode, why)
	}
	return true, fmt.Sprintf("GET %s → %d", c.URL, resp.StatusCode)
}
