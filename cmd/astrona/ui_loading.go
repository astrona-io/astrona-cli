package main

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func uiLoadingPhase(title string) bool {
	phase, _, _ := strings.Cut(title, " / ")
	return phase == "STARTUP" || phase == "SETUP" || phase == "GRADING" || phase == "DESTROY"
}

func uiPaneContentSize(width, height int, title string) (int, int) {
	w, h := uiPaneSize(width, height)
	if uiLoadingPhase(title) && w >= 48 && h >= 10 {
		return min(112, w-8) - 4, min(24, h-2) - 3
	}
	return w, h
}

type uiLoadingTick struct{}

func uiLoadingTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return uiLoadingTick{} })
}

func (m *uiPaneModel) loadingBackground() string {
	w, h := uiPaneSize(m.width, m.height)
	if m.background != "" {
		return workspaceFit(m.background, w, h)
	}
	rows := []string{workspaceMuted.Render("  " + uiRunColumns("LAB", "TYPE", "STATUS", "ACTIVE", w-6))}
	for _, run := range m.dashboard.runs {
		spent := "—"
		if run.activityTracked {
			spent = formatUptime(run.activeTime)
		}
		rows = append(rows, workspaceMuted.Render("  "+uiRunColumns(run.name, run.kind, run.status, spent, w-6)))
	}
	return workspacePanel("ACTIVE RUNS", strings.Join(rows, "\n"), w, h, workspaceBorder)
}

func uiOverlay(background, foreground string, width, height, x, y int) string {
	base := strings.Split(workspaceFit(background, width, height), "\n")
	for i, line := range strings.Split(foreground, "\n") {
		if y+i >= 0 && y+i < len(base) {
			base[y+i] = ansi.Cut(base[y+i], 0, x) + line + ansi.Cut(base[y+i], x+ansi.StringWidth(line), width)
		}
	}
	return strings.Join(base, "\n")
}

func (m *uiPaneModel) loadingView(content string) string {
	w, h := uiPaneSize(m.width, m.height)
	if m.emulator.Width() == w {
		return content
	}
	spinner := []string{"◐", "◓", "◑", "◒"}[m.animation%4]
	bar := strings.Repeat("─", m.animation%16) + "━━━━" + strings.Repeat("─", 15-m.animation%16)
	progress := workspaceFocus.Render(spinner+"  ["+bar+"]") + "  Working…"
	if m.ended {
		progress = workspaceDanger.Render("Stopped · Review the output below")
	}
	panel := workspacePanel(uiPaneLabel(m.title), progress+"\n"+content, m.emulator.Width()+4, m.emulator.Height()+3, workspaceFocus)
	return uiOverlay(m.loadingBackground(), panel, w, h, (w-m.emulator.Width()-4)/2, (h-m.emulator.Height()-3)/2)
}

// Preserve the confirmation's own layout and blank interior while showing
// the dashboard around it. Input still goes to the confirmation program.
func (m *uiPaneModel) confirmationView(content string) string {
	w, h := uiPaneSize(m.width, m.height)
	lines := strings.Split(workspaceFit(content, w, h), "\n")
	x, right, top, bottom := w, 0, h, 0
	for i, line := range lines {
		plain := ansi.Strip(line)
		if strings.TrimSpace(plain) == "" {
			continue
		}
		x = min(x, ansi.StringWidth(plain)-ansi.StringWidth(strings.TrimLeft(plain, " ")))
		right = max(right, ansi.StringWidth(strings.TrimRight(plain, " ")))
		top, bottom = min(top, i), i+1
	}
	if top >= bottom {
		return m.loadingBackground()
	}
	var panel []string
	for _, line := range lines[top:bottom] {
		panel = append(panel, ansi.Cut(line, x, right))
	}
	return uiOverlay(m.loadingBackground(), strings.Join(panel, "\n"), w, h, x, top)
}
