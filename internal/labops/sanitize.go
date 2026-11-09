package labops

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SanitizeTerminalText removes every control character except newline and
// tab — C0 (ESC included), DEL and C1. Lab docs can come from any URL or
// git repo; printed raw, an embedded escape sequence could rewrite the
// student's screen, retitle their terminal, or worse.
func SanitizeTerminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		// RuneError: an invalid UTF-8 byte — e.g. a raw 0x9b, which
		// 8-bit terminals treat as CSI (ESC [).
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}

// SanitizeLine is SanitizeTerminalText for a lab string printed inside one
// line — a resource's run command or description, a script name, a URL:
// newlines and tabs become spaces too, so the value can't fake or bury a
// line around it (the trust prompt's above all).
func SanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, SanitizeTerminalText(s))
}
