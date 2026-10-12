package main

import (
	"errors"
	"fmt"
	"io"
	"os"
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
	title        string
	summary      string
	details      string
	detailsTitle string
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
	approval, err := prepareUIRunApproval(cmd, flags)
	if err != nil {
		return err
	}
	yes, err := confirm(cmd, approval.notice)
	if err != nil {
		return err
	}
	if !yes {
		return errUIRunDeclined
	}
	if err := approval.save(cmd); err != nil {
		return err
	}
	flags.uiStartupApproved, flags.installVersion = true, true
	flags.uiRestart, flags.localOnly = approval.restart, approval.localOnly
	return nil
}

type uiRunApproval struct {
	view          settingsView
	store         terms.Store
	termsAccepted bool
	restart       *uiRunRestart
	localOnly     bool
	notice        uiRunNotice
}

func uiRunSettings(flags *rootFlags) (settingsView, error) {
	path, err := settingsPath(flags)
	if err != nil {
		return settingsView{}, err
	}
	return buildSettingsView(path)
}

func prepareUIRunApproval(cmd *cobra.Command, flags *rootFlags) (uiRunApproval, error) {
	var approval uiRunApproval
	view, err := uiRunSettings(flags)
	if err != nil {
		return approval, err
	}
	store, err := terms.DefaultStore()
	if err != nil {
		return approval, err
	}
	accepted, err := store.Accepted()
	if err != nil {
		return approval, err
	}
	restart, err := findUIRunRestart(cmd, flags, readHomeContainers)
	if err != nil {
		return approval, err
	}
	preview := *flags
	preview.uiRestart = restart
	requestedRestart, _ := cmd.Flags().GetBool("yes")
	preview.localOnly = restart != nil || requestedRestart
	approval = uiRunApproval{view: view, store: store, termsAccepted: accepted, restart: restart, localOnly: preview.localOnly, notice: buildUIRunNotice(cmd, &preview, view, !accepted)}
	return approval, nil
}

func (a uiRunApproval) save(cmd *cobra.Command) error {
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	// Use the displayed review ID; a concurrent settings edit must not be
	// approved using stale information. Keep the existing review lock and writes.
	review := &cobra.Command{}
	review.SetContext(cmd.Context())
	review.SetErr(io.Discard)
	if err := ensureSettingsReview(review, a.view.Path, reviewDefinitions(), false, a.view.ID); err != nil {
		return err
	}
	if !a.termsAccepted {
		return a.store.Accept("prompt", Version)
	}
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
	action := "Start this lab. If it already exists, review a restart confirmation."
	if restart, _ := cmd.Flags().GetBool("yes"); restart {
		action = "Restart this lab if it exists. Its current work will be deleted."
	}
	tracking := "Catalog labs use your signed-in account for progress tracking. If signed out, the lab runs without account tracking."
	title := "START LAB"
	if flags.uiRestart != nil {
		title, action = "RESTART LAB", flags.uiRestart.message()
		tracking = ""
	}
	if flags.localOnly && flags.uiRestart == nil {
		tracking = "This run is local only. No progress or results will be sent to the Astrona API."
	}
	var values, details []string
	for _, v := range view.Values {
		value, source := string(v.Value), v.Source
		if f := cmd.Flags().Lookup(v.Key); f != nil && f.Changed {
			value, source = f.Value.String(), "command flag"
		}
		if v.Key == "keep-context" && os.Getenv(labShellEnvVar) != "" {
			value, source = "true", "inherited lab shell"
		}
		values = append(values, v.Key+"="+settingValueText(value))
		details = append(details, fmt.Sprintf("%s: %s (%s; default %s)\n%s", v.Key, settingValueText(value), source, settingValueText(string(v.Default)), v.Description))
	}
	summary := sanitizeLine(label) + "\n\n" + action + "\n"
	if flags.uiRestart == nil {
		summary += "Other labs stay running.\n"
	}
	if needsTerms {
		summary += "Yes accepts the CLI terms and data-loss risk notice (Tab to read).\n"
	}
	summary += "\nYes authorizes downloads, required CLI installation, containers, virtual machines, lab scripts and manifests, and configured local ports. Lab code may run on your machine. Only accept if you trust this lab and its sources. This code approval applies to this run only.\n\n"
	if tracking != "" {
		summary += tracking + "\n\n"
	}
	summary += "Settings: " + strings.Join(values, ", ") + ". Saved choices are kept.\n"
	if len(view.Pending) > 0 {
		summary += "Yes acknowledges the settings review; removed settings are cleared.\n"
	}
	summary += "\nTab: terms and setting explanations. No cancels before startup."
	return uiRunNotice{title: title, summary: summary, details: terms.Document() + "\n\nSettings\n\n" + strings.Join(details, "\n\n") + "\n\nPending review: " + strings.Join(view.Pending, ", ")}
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
		case "tab", "shift+tab":
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
			m.scroll = len(m.noticeText())
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

func (m uiRunModel) noticeText() string {
	if m.details {
		return m.notice.details
	}
	return m.notice.summary
}

func (m uiRunModel) maxScroll() int {
	w := min(100, m.width-4)
	lines := strings.Split(ansi.Wrap(sanitizeTerminalText(m.noticeText()), max(1, w-4), ""), "\n")
	return max(0, len(lines)-max(1, m.height-9))
}

func (m uiRunModel) View() string {
	if m.width < 48 || m.height < 14 {
		return workspaceFit("ASTRONA / START LAB\nResize to 48 × 14.\nEsc cancels startup.", m.width, m.height)
	}
	w, h := min(100, m.width-4), m.height-4
	title := "START LAB"
	if m.notice.title != "" {
		title = m.notice.title
	}
	if m.details {
		title = "TERMS AND SETTINGS"
		if m.notice.detailsTitle != "" {
			title = m.notice.detailsTitle
		}
	}
	buttons := "  Yes    [No]"
	if m.yes {
		buttons = " [Yes]    No "
	}
	body := workspaceFit(workspacePage(m.noticeText(), w-4, h-5, m.scroll), w-4, h-5)
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
	uiPaneTitle(notice.title)
	defer uiPaneTitle("SETUP")
	final, err := runUIProgram(uiRunModel{notice: notice}, tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()), tea.WithAltScreen())
	if err != nil {
		return false, fmt.Errorf("startup confirmation: %w", err)
	}
	return final.(uiRunModel).accepted, nil
}
