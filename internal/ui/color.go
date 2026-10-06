package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Styles for Paint. Applied only when the target is a color terminal
// (screenIsColorTTY: never with NO_COLOR, never when piped or in a log).
const (
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	reset  = "\033[0m"
)

// Paint styles s for w — unchanged when w isn't a color terminal. Styles
// combine: Paint(w, s, Red, Bold).
func Paint(w io.Writer, s string, styles ...string) string {
	if !screenIsColorTTY(w) || len(styles) == 0 {
		return s
	}
	return strings.Join(styles, "") + s + reset
}

// Warnf prints "[WARN] …" to stderr, the tag in yellow.
func Warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", Paint(os.Stderr, "[WARN]", Yellow, Bold), fmt.Sprintf(format, a...))
}

// Infof prints "[INFO] …" to stderr, the tag in cyan.
func Infof(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", Paint(os.Stderr, "[INFO]", Cyan), fmt.Sprintf(format, a...))
}

// PrintError prints a command's error to stderr: "Error:" and its first
// line in red — what went wrong — and any following lines (hints, what
// to do next) in the normal color.
func PrintError(err error) {
	msg := err.Error()
	first, rest, _ := strings.Cut(msg, "\n")
	fmt.Fprintf(os.Stderr, "%s %s\n", Paint(os.Stderr, "Error:", Red, Bold), Paint(os.Stderr, first, Red))
	if rest != "" {
		fmt.Fprintln(os.Stderr, rest)
	}
}

// PassFail is "PASS"/"FAIL" (padded to width) in green/red for w.
func PassFail(w io.Writer, pass bool, width int) string {
	if pass {
		return Paint(w, fmt.Sprintf("%-*s", width, "PASS"), Green, Bold)
	}
	return Paint(w, fmt.Sprintf("%-*s", width, "FAIL"), Red, Bold)
}
