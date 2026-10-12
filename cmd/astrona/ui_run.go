package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"astrona/internal/terms"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

var errUIRunDeclined = errors.New("lab startup cancelled")

const uiRunDeclinedExitCode = 3

type uiRunNotice struct {
	summary string
	details string
}

// Install after the ordinary gates so UI consent happens before their prompts,
// settings application, catalog downloads or lab operations.
func installUIRunGate(root *cobra.Command, flags *rootFlags, confirm func(*cobra.Command, uiRunNotice) (bool, error)) {
	installCommandGate(root, func(cmd *cobra.Command) error {
		if cmd.Name() != "run" || cmd.Parent() != root {
			return nil
		}
		ui, err := cmd.Flags().GetBool("ui")
		if err != nil {
			return err
		}
		startup, err := cmd.Flags().GetBool("ui-startup")
		if err != nil || (!ui && !startup) {
			return err
		}
		return approveUIRun(cmd, flags, confirm)
	})
}

func approveUIRun(cmd *cobra.Command, flags *rootFlags, confirm func(*cobra.Command, uiRunNotice) (bool, error)) error {
	path, err := settingsPath(flags)
	if err != nil {
		return err
	}
	view, err := buildSettingsView(path)
	if err != nil {
		return err
	}
	store, err := terms.DefaultStore()
	if err != nil {
		return err
	}
	accepted, err := store.Accepted()
	if err != nil {
		return err
	}
	notice := buildUIRunNotice(cmd, flags, view, !accepted)
	yes, err := confirm(cmd, notice)
	if err != nil {
		return err
	}
	if !yes {
		return errUIRunDeclined
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	// Use the displayed review ID; a concurrent settings edit must not be
	// approved using stale information. Keep the existing review lock and writes.
	review := &cobra.Command{}
	review.SetContext(cmd.Context())
	review.SetErr(io.Discard)
	if err := ensureSettingsReview(review, path, reviewDefinitions(), false, view.ID); err != nil {
		return err
	}
	if !accepted {
		if err := store.Accept("prompt", Version); err != nil {
			return err
		}
	}
	flags.uiStartupApproved = true
	flags.installVersion = true
	return nil
}

func buildUIRunNotice(cmd *cobra.Command, flags *rootFlags, view settingsView, needsTerms bool) uiRunNotice {
	selected := *flags
	applyCurrentLab(cmd, &selected)
	label := selected.configPath
	if selected.catalogLab != "" {
		label = selected.catalogLab
	}
	if args := cmd.Flags().Args(); len(args) > 0 {
		label = args[0]
		if !cmd.Flags().Changed("git") {
			selected.gitURL, selected.gitRef = "", ""
		}
	}
	if selected.gitURL != "" {
		label += " from " + selected.gitURL
	}
	if selected.gitRef != "" {
		label += " at " + selected.gitRef
	}
	action := "Start this lab, or open its workspace if it is already running."
	if restart, _ := cmd.Flags().GetBool("yes"); restart {
		action = "Restart this lab if it exists. Its current work will be deleted."
	}
	var values, details []string
	for _, v := range view.Values {
		value, source := string(v.Value), v.Source
		if f := cmd.Flags().Lookup(v.Key); f != nil && f.Changed {
			value, source = f.Value.String(), "command flag"
		}
		values = append(values, v.Key+"="+settingValueText(value))
		details = append(details, fmt.Sprintf("%s: %s (%s; default %s)\n%s", v.Key, settingValueText(value), source, settingValueText(string(v.Default)), v.Description))
	}
	summary := sanitizeLine(label) + "\n\n" + action + "\nOther labs stay running.\n"
	if needsTerms {
		summary += "Yes accepts the CLI terms and data-loss risk notice (Tab to read).\n"
	}
	summary += "\n" +
		"Yes authorizes downloads, required CLI installation, containers, lab scripts and manifests, and configured local ports. Lab code may run on your machine. Only accept if you trust this lab and its sources. This code approval applies to this run only.\n\n" +
		"Catalog labs use your signed-in account for progress tracking. If signed out, the lab runs without account tracking.\n\n" +
		"Settings: " + strings.Join(values, ", ") + ". Saved choices are kept.\n"
	if len(view.Pending) > 0 {
		summary += "Yes acknowledges the settings review; removed settings are cleared.\n"
	}
	summary += "\nTab: terms and setting explanations. No cancels before startup."
	return uiRunNotice{summary: summary, details: terms.Document() + "\n\nSettings\n\n" + strings.Join(details, "\n\n") + "\n\nPending review: " + strings.Join(view.Pending, ", ")}
}

type uiRunModel struct {
	notice                 uiRunNotice
	width, height, scroll  int
	details, yes, accepted bool
}

func (m uiRunModel) Init() tea.Cmd { return nil }

func (m uiRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "n", "N", "esc", "q", "ctrl+c", "ctrl+d":
			return m, tea.Quit
		case "tab", "shift+tab", "f1":
			m.details, m.scroll = !m.details, 0
		case "up", "k":
			m.scroll = max(0, m.scroll-1)
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll = max(0, m.scroll-max(1, m.height-10))
		case "pgdown":
			m.scroll += max(1, m.height-10)
		case "home":
			m.scroll = 0
		case "end":
			m.scroll = len(m.text())
		case "left", "right", "h", "l":
			m.yes = !m.yes
		case "y", "Y", "enter":
			if m.width >= 48 && m.height >= 14 {
				m.accepted = m.yes || msg.String() != "enter"
				return m, tea.Quit
			}
		}
	}
	m.scroll = min(m.scroll, m.maxScroll())
	return m, nil
}

func (m uiRunModel) text() string {
	if m.details {
		return m.notice.details
	}
	return m.notice.summary
}

func (m uiRunModel) maxScroll() int {
	w := min(100, m.width-4)
	lines := strings.Split(ansi.Wrap(sanitizeTerminalText(m.text()), max(1, w-4), ""), "\n")
	return max(0, len(lines)-max(1, m.height-9))
}

func (m uiRunModel) View() string {
	if m.width < 48 || m.height < 14 {
		return workspaceFit("ASTRONA / START LAB\nResize to 48 × 14.\nEsc cancels startup.", m.width, m.height)
	}
	w, h := min(100, m.width-4), m.height-4
	title := "START LAB"
	if m.details {
		title = "TERMS AND SETTINGS"
	}
	buttons := "  Yes    [No]"
	if m.yes {
		buttons = " [Yes]    No "
	}
	body := workspaceFit(workspacePage(m.text(), w-4, h-5, m.scroll), w-4, h-5)
	body += "\n" + workspaceFocus.Render(buttons) + "\n" + workspaceMuted.Render("←/→ choose  enter confirm  y yes  n no")
	box := workspacePanel(title, body, w, h, workspaceFocus)
	footer := "↑/↓ scroll  Tab details  Esc cancel"
	if m.maxScroll() > 0 {
		footer += fmt.Sprintf("  [%d/%d]", m.scroll+1, m.maxScroll()+1)
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box+"\n"+workspaceFit(footer, w, 1))
}

func confirmUIRun(cmd *cobra.Command, notice uiRunNotice) (bool, error) {
	if err := requireWorkspaceTerminal(); err != nil {
		return false, err
	}
	final, err := tea.NewProgram(uiRunModel{notice: notice}, tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()), tea.WithAltScreen()).Run()
	if err != nil {
		return false, fmt.Errorf("startup confirmation: %w", err)
	}
	return final.(uiRunModel).accepted, nil
}
