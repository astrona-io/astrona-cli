package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	workspaceAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	workspaceMuted  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "245"})
	workspaceBorder = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "243", Dark: "239"})
	workspaceFocus  = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	workspaceWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	workspaceDanger = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
)

// Keep every screen inside the terminal, including wide Unicode and styled rows.
func workspaceFit(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	rows := make([]string, max(0, height))
	for i := range rows {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], max(0, width), "…")
		}
		rows[i] = line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	}
	return strings.Join(rows, "\n")
}

func workspacePanel(title, content string, width, height int, border lipgloss.Style) string {
	if width < 4 || height < 3 {
		return workspaceFit(content, width, height)
	}
	label := ansi.Truncate(" "+sanitizeLine(title)+" ", width-4, "…")
	space := max(0, width-2-ansi.StringWidth(label))
	lines := []string{border.Render("┌" + strings.Repeat("─", space/2) + label + strings.Repeat("─", space-space/2) + "┐")}
	for _, line := range strings.Split(workspaceFit(content, width-4, height-2), "\n") {
		lines = append(lines, border.Render("│")+" "+line+" "+border.Render("│"))
	}
	lines = append(lines, border.Render("└"+strings.Repeat("─", width-2)+"┘"))
	return strings.Join(lines, "\n")
}

func workspaceSelected(text string, width int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("117")).Bold(true).
		Render(workspaceFit("> "+sanitizeLine(text), width, 1))
}

func workspacePage(text string, width, height, offset int) string {
	lines := strings.Split(ansi.Wrap(sanitizeTerminalText(text), max(1, width), ""), "\n")
	start := min(max(0, offset), max(0, len(lines)-height))
	return strings.Join(lines[start:min(len(lines), start+max(0, height))], "\n")
}

func (m workspaceModel) frame(title, context, body string, footer []string) string {
	w, h := max(1, m.width), max(1, m.height)
	brand := workspaceAccent.Render(" ASTRONA ") + workspaceFocus.Render("/ "+title)
	version := workspaceMuted.Render(sanitizeLine(Version) + " ")
	gap := w - ansi.StringWidth(brand) - ansi.StringWidth(version)
	if gap > 0 {
		brand += strings.Repeat(" ", gap) + version
	}
	active := workspaceNav([]string{"Labs", "Preferences"}, []string{"<enter>", "<p>"}, 0)
	if m.showPreferences {
		active = workspaceNav([]string{"Preferences", "Back"}, []string{"", "<esc>"}, 0)
	} else if !m.home && len(m.labs) == 0 {
		selected := 0
		if m.results {
			selected = 1
		}
		active = workspaceNav([]string{"Task", "Results", "Shell", "Preferences"}, []string{"<enter>", "<tab>", "<s>", "<p>"}, selected)
		if w < 72 {
			active = workspaceNav([]string{"Task", "Results", "Shell", "Settings"}, []string{"↵", "⇥", "s", "p"}, selected)
		}
	}
	if m.navigation != "" {
		active = m.navigation
	}
	header := brand + "\n" + workspaceMuted.Render(" "+sanitizeLine(context)) + "\n" + active
	for len(footer) < 2 {
		footer = append(footer, "")
	}
	if os.Getenv(uiPaneEnv) == "1" {
		if !m.showPreferences && !m.exitMenu && !m.confirmDestroy {
			footer[0] = active
			if m.home && !strings.HasPrefix(footer[1], "pgup/pgdn") {
				footer[1] = "↑/↓ select  Enter open  Ctrl+D delete  q quit"
			}
		}
		return workspaceFit(workspaceMuted.Render(" "+sanitizeLine(context))+"\n"+
			workspaceFit(body, w, m.bodyHeight())+"\n"+footer[0]+"\n"+workspaceMuted.Render(footer[1]), w, h)
	}
	return workspaceFit(header, w, 3) + "\n" + workspaceFit(body, w, max(0, h-5)) + "\n" +
		workspaceFit(workspaceFocus.Render(footer[0])+"\n"+workspaceMuted.Render(footer[1]), w, 2)
}

func (m workspaceModel) bodyHeight() int {
	if os.Getenv(uiPaneEnv) == "1" {
		return max(3, m.height-3)
	}
	return max(3, m.height-5)
}

func (m workspaceModel) smallView() string {
	keys := "q exit menu\np preferences"
	if m.home {
		keys = "q quit"
	}
	if m.showPreferences {
		keys = "Esc back"
	}
	if m.exitMenu {
		keys = "k leave running\nenter return"
	}
	if m.confirmDestroy {
		keys = "y confirm\nenter cancel"
	}
	return workspaceFit("ASTRONA / WORKSPACE\nResize to 48 × 14.\n"+keys, max(1, m.width), max(1, m.height))
}

func workspaceAlert(message string, width int) string {
	return workspacePanel("ALERT [f1 details]", workspacePage(message, width-4, 2, 0), width, 4, workspaceWarn)
}

func (m workspaceModel) alertMessage() string {
	if m.showPreferences {
		if m.preferences.noticeError {
			return m.preferences.notice
		}
		return ""
	}
	if m.snapshot.blocked != "" {
		return m.snapshot.blocked
	}
	return m.notice
}

func (m workspaceModel) alertView(background string) string {
	w := min(76, m.width-6)
	page := workspacePage(m.alertMessage(), w-4, m.height-8, m.alertScroll)
	dialog := workspaceDialog(background, "ALERT", page, m.width, m.height-2, false)
	return dialog + "\n" + workspaceFit("Esc close   enter close\n↑/↓ or pgup/pgdn scroll message", m.width, 2)
}

// Draw a dialog over the current screen without changing its navigation state.
func workspaceDialog(background, title, content string, width, height int, danger bool) string {
	w := min(76, max(4, width-6))
	lines := strings.Split(ansi.Wrap(sanitizeTerminalText(content), max(1, w-4), ""), "\n")
	h := min(height, len(lines)+2)
	style := workspaceFocus
	if danger {
		style = workspaceDanger
	}
	dialog := strings.Split(workspacePanel(title, strings.Join(lines, "\n"), w, h, style), "\n")
	if height-h < 2 {
		background = ""
	}
	base := strings.Split(workspaceFit(background, width, height), "\n")
	x, y := (width-w)/2, (height-h)/2
	for i, line := range dialog {
		base[y+i] = ansi.Cut(base[y+i], 0, x) + line + ansi.Cut(base[y+i], x+w, width)
	}
	return strings.Join(base, "\n")
}

func (m workspaceModel) labView() string {
	w, h := m.width, m.bodyHeight()
	clock := "Elapsed: unknown"
	if !m.snapshot.started.IsZero() {
		clock = "Elapsed: " + max(time.Duration(0), m.now.Sub(m.snapshot.started)).Round(time.Second).String()
	}
	if m.snapshot.activityTracked {
		clock = "Time spent: " + formatUptime(m.snapshot.activeTime)
	}
	if !m.snapshot.deadline.IsZero() {
		remaining := max(time.Duration(0), m.snapshot.deadline.Sub(m.now)).Round(time.Second)
		clock += "  Left: " + remaining.String()
	}
	context := m.name + "  |  " + m.snapshot.kind + "  |  " + clock
	if os.Getenv(uiPaneEnv) == "1" {
		context = fmt.Sprintf("%s  ·  Clusters: %d  ·  %s", m.snapshot.kind, len(m.snapshot.clusters), clock)
	}
	if m.snapshot.tracking != "" {
		context += "  |  " + m.snapshot.tracking
	}
	message := m.notice
	if message == "" {
		message = m.snapshot.activityError
	}
	if m.snapshot.blocked != "" {
		message = m.snapshot.blocked
	}
	if message != "" {
		h -= 4
	}
	leftWidth := min(34, max(20, w/3))
	rightWidth := w - leftWidth
	var rows []string
	visible := max(1, (h-2)/2)
	start := max(0, m.selected-visible+1)
	for i := start; i < min(len(m.snapshot.clusters), start+visible); i++ {
		c := m.snapshot.clusters[i]
		name := "  " + sanitizeLine(c.name)
		if i == m.selected {
			name = workspaceSelected(c.name, leftWidth-4)
		}
		rows = append(rows, name, workspaceMuted.Render("  "+sanitizeLine(c.status)))
	}
	left := workspacePanel(fmt.Sprintf("CLUSTERS [%d]", len(m.snapshot.clusters)), strings.Join(rows, "\n"), leftWidth, h, workspaceFocus)
	content, title := m.task, "THE TASK"
	if m.results {
		content, title = m.snapshot.result, "SUBMISSION RESULTS"
		if m.submission != "" {
			content = m.submission
		}
	}
	if content == "" {
		content = "No task document provided."
	}
	right := workspacePanel(title, workspacePage(content, rightWidth-4, h-2, m.scroll), rightWidth, h, workspaceBorder)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	if message != "" {
		body += "\n" + workspaceAlert(message, w)
	}
	footer := []string{"↑/↓ cluster  s shell  tab view  p preferences", "q exit menu  Ctrl+C overview  enter task"}
	if m.canSubmit {
		footer[1] = "g submit   " + footer[1]
	}
	if m.exitMenu {
		body = workspaceDialog(body, "LEAVE WORKSPACE", "The lab can keep running after you close this workspace.\n\n"+m.menuText(), w, m.bodyHeight(), false)
		footer = []string{"k leave running   d destroy   enter return", "Esc return to the lab"}
		if m.canSubmit {
			footer[1] = "s submit   " + footer[1]
		}
	}
	if m.confirmDestroy {
		body = workspaceDialog(body, "DESTROY LAB", "Destroy "+sanitizeLine(m.name)+" and its linked clusters?\n\nAll work inside them is lost.", w, m.bodyHeight(), true)
		footer = []string{"y confirm / enter cancel", "n or Esc keeps the lab running"}
	}
	return m.frame("LAB WORKSPACE", context, body, footer)
}
