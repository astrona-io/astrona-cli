package main

import (
	"fmt"
	"strings"
	"time"
)

// Expected time is guidance from the task document, never an expiry deadline.
type uiLabProgress struct {
	Lab      bool
	Expected time.Duration
}

func labTaskDocument(text string) (string, time.Duration) {
	meta, body := docFrontMatter(sanitizeTerminalText(text))
	if title, ok := meta["title"].(string); ok && strings.TrimSpace(title) != "" {
		body = "# " + sanitizeLine(strings.TrimSpace(title)) + "\n\n" + body
	}
	estimate, _ := meta["estimated_duration"].(string)
	expected, err := time.ParseDuration(strings.TrimSpace(estimate))
	if err != nil || expected <= 0 {
		expected = 0
	}
	return body, expected
}

func (m *uiPaneModel) labTimeProgress() string {
	_, name, _ := strings.Cut(m.title, " / ")
	if !m.labProgress.Lab || name != m.activity.target.Lab || name == "" ||
		(!m.taskPane() && !strings.HasPrefix(m.title, "WORKSPACE / ")) {
		return ""
	}
	used, expected := m.timeSpent(), m.labProgress.Expected
	text := "Used " + formatUptime(used)
	if expected <= 0 {
		text += " · Expected time not provided"
	} else {
		text += " / Expected " + formatUptime(expected)
		if used > expected {
			text += " · +" + formatUptime(used-expected) + " over estimate"
		}
	}
	if m.activity.paused {
		text += " · Paused"
	}
	if expected <= 0 {
		return text
	}
	// Convert before multiplying so even large author-provided durations fit.
	filled := min(16, max(0, int(float64(min(used, expected))/float64(expected)*16)))
	bar := fmt.Sprintf("[%s%s] ", strings.Repeat("━", filled), strings.Repeat("─", 16-filled))
	return workspaceFocus.Render(bar) + text
}
