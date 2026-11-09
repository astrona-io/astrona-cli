// Package workspace is the local HTTP server behind the browser workspace.
//
// It is the top internal layer: it may import labops, authoring and anything
// below them, and only cmd/astrona imports it. The terminal bridge, session
// state and frontend plug into it; this package owns the listener and the
// rules every request passes through.
//
// # Security contract
//
// Binding to loopback is not enough on its own, because any web page the user
// visits can send requests to 127.0.0.1. The server therefore guarantees:
//
//   - Loopback only. The listener is always 127.0.0.1 (IPv4 loopback), on the
//     configured port or a free one when that is taken. There is no way to
//     configure another address, so 0.0.0.0 cannot be reached by mistake.
//     Requests whose peer is not a loopback address are refused as well.
//   - Launch codes. Each server start creates a fresh random 256-bit
//     secret, kept only in memory and in server.json (mode 0600, in a 0700
//     directory owned by the user). A secret is never reused across starts,
//     so every code minted for an earlier start fails. A launch code is a
//     random nonce plus an expiry, authenticated with HMAC-SHA-256 under that
//     secret, so the CLI process can mint one without talking to the server.
//     A code is valid for at most MaxCodeTTL, and only once: the server
//     records each nonce it accepts until the code would have expired. The
//     code travels in the URL fragment (#code=…), which browsers never send
//     to a server or put in a Referer. The CLI mints a code only when the
//     process recorded in server.json is alive (ServerInfo.Alive).
//   - Session tokens. POST /api/v1/auth/exchange turns a valid code into a
//     random 256-bit token. Every other /api/v1 route requires it as
//     "Authorization: Bearer <token>"; tokens are compared in constant time
//     against stored SHA-256 digests. There are no cookies: a cookie for
//     127.0.0.1 would be sent to every local port, while the page keeps the
//     token in origin-scoped storage, which includes the port.
//   - Tickets. WebSockets and event streams cannot carry an Authorization
//     header, so the page fetches a single-use ticket (valid TicketTTL) with
//     its token and passes it as ?ticket=… on that one request.
//   - Host allowlist. A request is served only when its Host header is
//     exactly 127.0.0.1:<port> or localhost:<port>, which defeats DNS
//     rebinding (a rebound name still carries its own name in Host).
//   - Origin checks. A request carrying an Origin other than
//     http://127.0.0.1:<port> or http://localhost:<port> is refused, on every
//     route. WebSocket upgrades must carry an allowed Origin. Mutating
//     requests from a browser (Sec-Fetch-Site other than same-origin or none)
//     are refused even without an Origin. No CORS headers are ever sent and
//     preflight (OPTIONS) requests are refused.
//   - Response headers. Every response carries a Content Security Policy
//     that allows only the server's own origin (no inline script, no
//     external resources, no framing), plus nosniff, X-Frame-Options DENY,
//     Referrer-Policy no-referrer, same-origin COOP/CORP, a restrictive
//     Permissions-Policy and Cache-Control no-store.
//
// Nothing here reads lab files, validation scripts or solutions, and no
// response ever contains them; later routes must keep that rule.
package workspace
