package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"astrona/internal/account"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type guestRunChoice struct{ accepted, login, never bool }
type guestRunModel struct {
	width, height, focus int
	choice               guestRunChoice
	local                bool
}

func (m guestRunModel) Init() tea.Cmd { return nil }
func (m guestRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.Paste {
			return m, nil
		}
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "down":
			m.focus = (m.focus + 1) % 3
		case "shift+tab", "left", "up":
			m.focus = (m.focus + 2) % 3
		case " ":
			if m.focus == 2 {
				m.choice.never = !m.choice.never
			}
		case "enter":
			if m.width < 48 || m.height < 14 {
				return m, nil
			}
			if m.focus == 2 {
				m.choice.never = !m.choice.never
				return m, nil
			}
			m.choice.accepted, m.choice.login = true, m.focus == 0
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m guestRunModel) View() string {
	if m.width < 48 || m.height < 14 {
		return workspaceFit("Resize to 48 × 14. Esc cancels startup.", m.width, m.height)
	}
	text := "You're not logged in. Log in to track your lab progress and results on Astrona."
	if m.local {
		text = "You're not logged in. Log in to track progress on catalog labs. This local lab will stay local."
	}
	buttons := []string{"Log in", "Continue as guest"}
	for i := range buttons {
		if i == m.focus {
			buttons[i] = "[ " + buttons[i] + " ]"
		}
	}
	check := "[ ] Never ask again"
	if m.choice.never {
		check = "[x] Never ask again"
	}
	if m.focus == 2 {
		check = "> " + check
	}
	width := min(68, m.width-4)
	body := workspacePage(text, width-4, 4, 0) + "\n\n" + strings.Join(buttons, "    ") + "\n\n" + check + "\n\nTab/arrows choose · Space checks · Enter accepts\nEsc cancels this run"
	box := workspacePanel("TRACK YOUR PROGRESS", body, width, 14, workspaceFocus)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func guestReminderEnabled(flags *rootFlags) (bool, error) {
	path, err := settingsPath(flags)
	if err != nil {
		return false, err
	}
	saved, err := loadSettings(path)
	if err != nil {
		return false, err
	}
	value, err := saved.value("login-reminder")
	return string(value) == "true", err
}

func saveGuestChoice(flags *rootFlags, choice guestRunChoice) error {
	if !choice.accepted || !choice.never {
		return nil
	}
	path, err := settingsPath(flags)
	if err != nil {
		return err
	}
	value := "false"
	return changeSetting(path, "login-reminder", &value)
}

// Explicit local-only restarts never access credentials or the account API.
func confirmGuestLabRun(cmd *cobra.Command, flags *rootFlags, acct *labAccount, withoutLogin bool) (*labAccount, bool, error) {
	if flags.localOnly || flags.testMode || acct != nil {
		return acct, true, nil
	}
	tty := isatty.IsTerminal(os.Stdin.Fd())
	if withoutLogin || !tty {
		approved, err := approveUntrackedLab(cmd.InOrStdin(), cmd.ErrOrStderr(), tty, withoutLogin, flags.catalogLab, acct)
		return acct, approved, err
	}
	enabled, err := guestReminderEnabled(flags)
	if err != nil || !enabled {
		return acct, err == nil, err
	}
	if flags.catalogLab == "" {
		_, store, site, err := accountDeps()
		if err != nil {
			return nil, false, err
		}
		creds, err := store.Load()
		if err != nil {
			return nil, false, err
		}
		if creds != nil && creds.Site == site {
			return acct, true, nil
		}
	}
	uiPaneTitle("TRACK YOUR PROGRESS")
	defer uiPaneTitle("SETUP")
	result, err := runUIProgram(guestRunModel{local: flags.catalogLab == ""}, tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()), tea.WithAltScreen())
	if err != nil {
		return nil, false, fmt.Errorf("login reminder: %w", err)
	}
	choice := result.(guestRunModel).choice
	if err := saveGuestChoice(flags, choice); err != nil {
		return nil, false, err
	}
	if !choice.accepted || !choice.login {
		return nil, choice.accepted, nil
	}
	client, store, site, err := accountDeps()
	if err != nil {
		return nil, false, err
	}
	uiPaneTitle("SETUP / SIGN IN")
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	return loginForRun(ctx, flags, client, store, site)
}

func loginForRun(ctx context.Context, flags *rootFlags, client *account.Client, store account.Store, site string) (*labAccount, bool, error) {
	if err := login(ctx, client, store, site, thisDevice()); err != nil {
		return nil, false, err
	}
	acct, err := accountForLab(ctx, flags)
	return acct, err == nil, err
}
