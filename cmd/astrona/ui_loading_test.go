package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestUILoadingKeepsDashboardAndOutput(t *testing.T) {
	m := newUIPaneModel(nil, nil, 120, 30)
	defer m.emulator.Close()
	m.title = "SETUP"
	_, _ = m.emulator.WriteString("Downloading repository…\r\nContinue? [yes/no]")
	view := ansi.Strip(m.View())
	for _, want := range []string{"ACTIVE RUNS", "Setting up your lab", "Downloading repository", "Continue? [yes/no]", "Working"} {
		if !strings.Contains(view, want) {
			t.Fatalf("loading view hides %q", want)
		}
	}
	m.Update(uiLoadingTick{})
	if ansi.Strip(m.View()) == view {
		t.Fatal("progress did not animate")
	}
	if strings.Contains(m.View(), "100%") {
		t.Fatal("progress invented a percentage")
	}
	for _, title := range []string{"START LAB", "SETUP", "WORKSPACE / lab", "SHELL / lab", "HOME"} {
		m.title = title
		m.resizeContent()
		w, h := uiPaneContentSize(m.width, m.height, title)
		if m.emulator.Width() != w || m.emulator.Height() != h {
			t.Fatal("phase changed without resizing terminal")
		}
		lines := strings.Split(ansi.Strip(m.View()), "\n")
		if len(lines) != m.height {
			t.Fatal("phase overflows terminal")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatal("phase overflows width")
			}
		}
	}
}
