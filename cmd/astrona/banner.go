package main

import (
	"os"
	"strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
)

// colorsEnabled follows the two conventions a terminal tool is expected to
// respect: https://no-color.org/, and never emitting escape codes when
// stdout isn't an interactive terminal (piped to a file, captured in CI
// logs, etc.) — raw ANSI codes in non-TTY output are a correctness bug,
// not a style choice.
func colorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	return (info.Mode() & os.ModeCharDevice) != 0
}

func colorize(code, text string) string {
	if !colorsEnabled() {
		return text
	}
	return code + text + ansiReset
}

// banner renders the boxed "ASTRONA CLI" header shown above every help
// screen. Built from title length rather than hardcoded ASCII art so the
// box borders can never drift out of alignment with the title.
func banner() string {
	inner := "  ASTRONA CLI  "
	border := "╔" + strings.Repeat("═", len(inner)) + "╗"
	closeBorder := "╚" + strings.Repeat("═", len(inner)) + "╝"

	lines := []string{
		border,
		"║" + inner + "║",
		closeBorder,
	}

	return colorize(ansiCyan+ansiBold, strings.Join(lines, "\n"))
}

// supportLine is appended to the root command's Long description, so it
// shows on `astrona` and `astrona --help` without repeating on every
// subcommand's help screen.
func supportLine() string {
	return colorize(ansiYellow, "Astrona is free and built on donations and community support — thank you to everyone who contributes, sponsors, and helps keep it going.")
}
