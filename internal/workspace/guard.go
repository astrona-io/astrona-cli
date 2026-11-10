package workspace

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ContentSecurityPolicy allows the page to load and talk to its own origin
// only: no inline script or style, no external resources, no framing. The
// page itself (index.html) also gets a frame-src naming exactly the origins
// of the current runs' page tabs (see Server.pagePolicy).
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; font-src 'self'; connect-src 'self'; media-src 'none'; " +
	"object-src 'none'; worker-src 'none'; manifest-src 'none'; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'none'"

// securityHeaders are set on every response, errors included.
var securityHeaders = map[string]string{
	"Content-Security-Policy":      ContentSecurityPolicy,
	"X-Content-Type-Options":       "nosniff",
	"X-Frame-Options":              "DENY",
	"Referrer-Policy":              "no-referrer",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"Cross-Origin-Resource-Policy": "same-origin",
	"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), usb=(), serial=(), hid=(), payment=()",
	"Cache-Control":                "no-store",
}

// origins holds what a request to this listener may name itself as.
type origins struct {
	hosts   [2]string // Host header values
	origins [2]string // Origin header values
}

func newOrigins(port int) origins {
	p := strconv.Itoa(port)
	return origins{
		hosts:   [2]string{"127.0.0.1:" + p, "localhost:" + p},
		origins: [2]string{"http://127.0.0.1:" + p, "http://localhost:" + p},
	}
}

func (o origins) hostAllowed(host string) bool {
	return strings.EqualFold(host, o.hosts[0]) || strings.EqualFold(host, o.hosts[1])
}

func (o origins) originAllowed(origin string) bool {
	return strings.EqualFold(origin, o.origins[0]) || strings.EqualFold(origin, o.origins[1])
}

// guard wraps next with the checks every request passes before routing:
// security headers, loopback peer, Host allowlist, Origin and Sec-Fetch-Site,
// and the allowed methods. It never sets CORS headers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for k, v := range securityHeaders {
			h.Set(k, v)
		}
		if !loopbackPeer(r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if !s.origins.hostAllowed(r.Host) {
			// DNS rebinding: a page on another name resolved to 127.0.0.1.
			writeError(w, http.StatusMisdirectedRequest, "unexpected host")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete:
		default:
			// OPTIONS included: there is no CORS, so preflights fail.
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		origin, hasOrigin := r.Header["Origin"]
		if hasOrigin && (len(origin) != 1 || !s.origins.originAllowed(origin[0])) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if isUpgrade(r) && !hasOrigin {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || isUpgrade(r) {
			// Browsers send Sec-Fetch-Site even where they omit Origin.
			switch r.Header.Get("Sec-Fetch-Site") {
			case "", "same-origin", "none":
			default:
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		s.touch()
		next.ServeHTTP(w, r)
	})
}

func isUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" || headerHasToken(r.Header, "Connection", "upgrade")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func loopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireToken admits a request only with a valid bearer token.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok || !s.auth.validToken(token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="astrona-workspace"`)
			writeError(w, http.StatusUnauthorized, "missing or invalid token")
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	v := r.Header.Values("Authorization")
	if len(v) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(v[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
