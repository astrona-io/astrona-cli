// Package account signs the CLI in to an Astrona site, keeps the
// credentials on disk, and asks the site for lab sessions.
//
// Sign-in is a device-code flow run by the site itself: the CLI asks the
// site for a code, the student authorizes it on the site's own page (signing
// in there first if needed), and the CLI polls until the site issues its
// tokens. The tokens are opaque to the CLI.
//
// Tokens are secrets: nothing in this package prints or logs them, and they
// are only ever sent to the site they were obtained from.
package account

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultSite is the Astrona site the CLI signs in to unless ASTRONA_URL,
// `astrona login --site` or an earlier sign-in says otherwise.
const DefaultSite = "https://astrona.io"

// SiteEnv names the environment variable that overrides DefaultSite (for
// example http://localhost:3000 for local development).
const SiteEnv = "ASTRONA_URL"

// CheckHTTPURL accepts only an absolute http(s) URL with a host and no
// embedded username or password.
func CheckHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed, got %q", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("the URL has no host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("the URL must not contain a username or password")
	}
	return u, nil
}

// checkTokenURL is CheckHTTPURL for a URL tokens are sent to: plain http is
// only allowed to this machine (local development), never across a network.
func checkTokenURL(raw string) (*url.URL, error) {
	u, err := CheckHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("%s must use https — plain http is only allowed for localhost", u.Redacted())
	}
	return u, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ResolveSite returns the site the CLI talks to: ASTRONA_URL when set
// (validated), else saved — the site of the saved sign-in, "" when there
// is none — else DefaultSite. getenv is os.Getenv outside tests.
func ResolveSite(getenv func(string) string, saved string) (string, error) {
	if raw := strings.TrimSpace(getenv(SiteEnv)); raw != "" {
		site, err := NormalizeSite(raw)
		if err != nil {
			return "", fmt.Errorf("%s=%q: %w", SiteEnv, raw, err)
		}
		return site, nil
	}
	if saved != "" {
		if site, err := NormalizeSite(saved); err == nil {
			return site, nil
		}
	}
	return DefaultSite, nil
}

// NormalizeSite validates a site URL and reduces it to scheme://host[:port]
// (any path, query or fragment is rejected rather than silently dropped).
func NormalizeSite(raw string) (string, error) {
	u, err := checkTokenURL(raw)
	if err != nil {
		return "", err
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("give only the site's address, like https://astrona.io")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// IsDefaultSite reports whether site is the official Astrona site
// (DefaultSite, with or without "www."). Anything else — a local or
// self-hosted Astrona (it is open source) — is worth telling the user about
// before data is sent to it.
func IsDefaultSite(site string) bool {
	u, err := url.Parse(strings.TrimSpace(site))
	if err != nil || u.Host == "" {
		return false
	}
	return SameSite(u, DefaultSite)
}

// sameOrigin reports whether a and b have the same scheme and host:port.
func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

// SameSite reports whether page belongs to site: the same scheme and host,
// allowing a "www." difference (astrona.io vs www.astrona.io).
func SameSite(page *url.URL, site string) bool {
	s, err := url.Parse(site)
	if err != nil || page.Scheme != s.Scheme || page.Port() != s.Port() {
		return false
	}
	strip := func(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }
	return strip(page.Hostname()) == strip(s.Hostname())
}
