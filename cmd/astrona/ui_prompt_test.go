package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUITerminalCompactPromptKeepsShellSetup(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "sh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell not installed")
			}
			home := t.TempDir()
			dotDir := filepath.Join(home, "shell's settings")
			if err := os.MkdirAll(dotDir, 0700); err != nil {
				t.Fatal(err)
			}
			body := "alias kept_alias=\"printf 'setup-%s\\\\n' kept\"\nPS1='long-host-and-context> '\n"
			files := map[string]string{
				filepath.Join(home, ".bashrc"):   body + "PROMPT_COMMAND=('PS1=long-theme-prompt')\n",
				filepath.Join(home, "shrc"):      body,
				filepath.Join(dotDir, ".zshenv"): "export ZSHENV_KEPT=yes\n",
				filepath.Join(dotDir, ".zshrc"):  body + "function theme_prompt() { PROMPT=long-theme-prompt; RPROMPT=long-right-prompt; }; precmd_functions=(theme_prompt)\n",
			}
			for name, content := range files {
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			manager := newUITerminals(t.Context(), func(string) (*exec.Cmd, error) {
				cmd := exec.Command(path)
				cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+dotDir, "ENV="+filepath.Join(home, "shrc"), "KUBECONFIG=/tmp/lab-config", "TERM=xterm-256color")
				return cmd, nil
			})
			manager.compactPrompt = true
			t.Cleanup(manager.close)
			if result := manager.start("", 100, 24)().(uiTerminalStarted); result.err != nil {
				t.Fatal(result.err)
			}
			tab := manager.snapshot()[0]
			waitUITab(t, tab, "astrona-cli @")
			if shell != "fish" {
				_, _ = io.WriteString(tab.terminal, "kept_alias\n")
				waitUITab(t, tab, "setup-kept")
			}
			_, _ = io.WriteString(tab.terminal, "printf 'context-%s\\n' \"$KUBECONFIG\"\n")
			waitUITab(t, tab, "context-/tmp/lab-config")
			if shell == "zsh" {
				_, _ = io.WriteString(tab.terminal, "printf 'env-%s\\n' \"$ZSHENV_KEPT\"\n")
				waitUITab(t, tab, "env-yes")
			}
			tab.mu.Lock()
			screen := uiTerminalScreen(tab.emulator, 0, false)
			tab.mu.Unlock()
			if strings.Contains(screen, "long-theme-prompt") || strings.Contains(screen, "long-right-prompt") {
				t.Fatal("shell theme replaced the compact prompt")
			}
			manager.close()
			for name, content := range files {
				if data, err := os.ReadFile(name); err != nil || string(data) != content {
					t.Fatalf("shell configuration changed: %s", name)
				}
			}
		})
	}
}
