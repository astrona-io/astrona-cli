package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
	"astrona/internal/labstate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestUIAccountStatus(t *testing.T) {
	for _, tc := range []struct {
		name, site, file, label           string
		saved, expired, signedIn, warning bool
	}{
		{name: "signed out", label: "Not logged in"},
		{name: "signed in", saved: true, label: "Logged in", signedIn: true},
		{name: "expired", saved: true, expired: true, label: "Login needs checking", warning: true},
		{name: "different site", saved: true, site: "https://other.example", label: "Not logged in"},
		{name: "broken file", file: "{broken", label: "Login unavailable", warning: true},
		{name: "old format", file: `{"issuer":"https://old.example"}`, label: "Not logged in"},
		{name: "invalid site", site: "invalid", label: "Check site settings", warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			site := "https://astrona.example"
			if tc.site != "" {
				site = tc.site
			}
			t.Setenv("ASTRONA_URL", site)
			store, err := account.DefaultStore()
			if err != nil {
				t.Fatal(err)
			}
			if tc.saved || tc.file != "" {
				expires := time.Now().Add(time.Hour)
				if tc.expired {
					expires = time.Now().Add(-time.Hour)
				}
				err := store.Save(&account.Credentials{Site: "https://astrona.example", AccessToken: "secret-access", RefreshToken: "secret-refresh", Username: "learner", AccessExpiresAt: expires})
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.file != "" {
				if err := os.WriteFile(store.Path, []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := readUIAccountStatus().(uiAccountStatus)
			if got.label != tc.label || got.signedIn != tc.signedIn || got.warning != tc.warning {
				t.Fatalf("status = %+v", got)
			}
			if tc.signedIn && got.username != "learner" {
				t.Fatalf("account name = %q", got.username)
			}
			// Refresh must be read-only, including when a token has expired.
			before, _ := os.ReadFile(store.Path)
			readUIAccountStatus()
			after, _ := os.ReadFile(store.Path)
			if string(before) != string(after) {
				t.Fatal("header changed saved credentials")
			}
		})
	}
}

func TestUIHeaderKeepsLoginVisible(t *testing.T) {
	for _, width := range []int{32, 48, 80, 120, 160} {
		m := &uiPaneModel{width: width, height: 30, account: uiAccountStatus{label: "Logged in", signedIn: true, username: strings.Repeat("界", 40) + "\x1b[2J", site: "astrona.example"}}
		view := m.header()
		if strings.Contains(view, "\x1b[2J") || !strings.Contains(ansi.Strip(view), "Logged in") {
			t.Fatalf("unsafe or missing login: %q", view)
		}
		lines := strings.Split(view, "\n")
		if len(lines) != uiHeaderHeight(width, m.height) {
			t.Fatal("header changed the child terminal height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != width {
				t.Fatalf("header width: %d, want %d", ansi.StringWidth(line), width)
			}
		}
	}
}

func TestUIHomeNavigationShortcuts(t *testing.T) {
	m := uiHomeModel{width: 100, height: 24}
	for _, key := range []string{"2", "3", "1"} {
		m = homeKey(m, key)
		if m.tab != int(key[0]-'1') || m.open || m.quit {
			t.Fatalf("shortcut %s: tab=%d, open=%v, quit=%v", key, m.tab, m.open, m.quit)
		}
	}
	m = homeKey(m, "n")
	m = homeKey(m, "2")
	if m.tab != 1 || m.open || m.quit {
		t.Fatal("removed start key intercepted navigation")
	}
}

func TestUIOverviewLayout(t *testing.T) {
	t.Setenv(uiPaneEnv, "1")
	for _, size := range [][2]int{{80, 24}, {100, 30}, {160, 35}} {
		pane := newUIPaneModel(nil, nil, size[0], size[1])
		pane.title = "HOME"
		pane.resizeContent()
		pane.account = uiAccountStatus{label: "Not logged in", site: "astrona.example"}
		home := uiHomeModel{width: pane.emulator.Width(), height: pane.emulator.Height(), data: uiHomeSnapshot{runs: []uiHomeRun{
			{name: strings.Repeat("long-lab-name-", 10), kind: "Lab", status: "Running", openable: true},
			{name: "astro-storage", kind: "Lab", status: "Stopped"},
		}}}
		_, _ = pane.emulator.WriteString("\x1b[?25l" + strings.ReplaceAll(home.View(), "\n", "\r\n"))
		view := ansi.Strip(pane.View())
		for _, want := range []string{"Not logged in", "LAB", "TYPE", "STATUS", "ACTIVE", "Running", "Stopped", "<1>"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%dx%d: missing %q", size[0], size[1], want)
			}
		}
		if strings.Count(view, "┌") != 1 {
			t.Fatal("overview should have a single table border")
		}
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatal("overview overflowed terminal height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != size[0] {
				t.Fatal("overview overflowed terminal width")
			}
		}
		_ = pane.emulator.Close()
	}
}

func TestUILabResourcesBeforeLogo(t *testing.T) {
	for _, width := range []int{48, 64, 80, 100, 120, 160, 200} {
		m := &uiPaneModel{width: width, height: 35, title: "TASK / astro-lab", account: uiAccountStatus{label: "Logged in", signedIn: true}}
		m.activity.target.Lab = "astro-lab"
		m.usage = labstate.ResourceUsage{CPU: 12.5, Memory: 512 * (1 << 20), AverageCPU: 10, AverageMemory: 256 * (1 << 20), Samples: 2, Updated: time.Now()}
		view := ansi.Strip(m.header())
		if !strings.Contains(view, "12.5%") {
			t.Fatalf("resources missing at %d: %s", width, view)
		}
		if width >= 64 {
			for _, want := range []string{"CPU avg", "Memory", "Mem avg", "512 MiB"} {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q at %d", want, width)
				}
			}
		}
		lines := strings.Split(view, "\n")
		for _, line := range lines {
			if ansi.StringWidth(line) != width {
				t.Fatal("header width overflow")
			}
		}
		if width >= 160 {
			if cpu, logo := strings.Index(lines[0], "CPU:"), strings.Index(lines[0], "___"); cpu < 0 || logo < cpu {
				t.Fatalf("resources are not left of logo: %s", lines[0])
			}
		}
		m.title = "TASK / astro-other"
		if strings.Contains(ansi.Strip(m.header()), "12.5%") {
			t.Fatal("new lab inherited previous lab's resources")
		}
	}
}

func TestHomeHasNoStartupAction(t *testing.T) {
	m := uiHomeModel{width: 100, height: 30}
	for _, key := range []string{"n", "enter"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if key == "enter" {
			next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		}
		m = next.(uiHomeModel)
		if cmd != nil || m.open || m.editor.action != workspaceNone {
			t.Fatal("Home still starts a lab")
		}
	}
	for _, view := range []string{m.View(), (&uiPaneModel{title: "HOME", width: 160, height: 35}).header()} {
		plain := ansi.Strip(view)
		if strings.Contains(plain, "n start") || strings.Contains(plain, "Start lab") {
			t.Fatal("Home advertises removed startup action")
		}
	}
}
