package labops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Notify shows a desktop notification: on macOS through osascript
// (display notification), on Linux through notify-send. Without the tool
// it does nothing. It never blocks: the notifier runs in the background
// and is stopped after a few seconds.
func Notify(title, message string) {
	if testing.Testing() {
		return
	}
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "osascript"
		args = []string{"-e", "display notification " + appleScriptString(message) + " with title " + appleScriptString(title)}
	case "linux":
		name = "notify-send"
		args = []string{"--app-name=astrona", title, message}
	default:
		return
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, path, args...).Run()
	}()
}

// appleScriptString quotes s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", " ", "\r", " ").Replace(s)
	return "\"" + s + "\""
}

// ExamNoticeMinutes are the minutes left at which the exam watchdog shows a
// countdown notice.
var ExamNoticeMinutes = []int{60, 30, 15, 5, 1}

// examNotice is the countdown notice due with left on the clock: the
// smallest of ExamNoticeMinutes that left is within (0: none, more than an
// hour left).
func examNotice(left time.Duration) int {
	due := 0
	for _, m := range ExamNoticeMinutes {
		if left <= time.Duration(m)*time.Minute {
			due = m
		}
	}
	return due
}

// shellHistoryFiles are the shell history files whose modification time
// tells that the student is typing commands: zsh, bash and fish, and
// $HISTFILE. Only their modification times are read, never their contents.
func shellHistoryFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	files := []string{
		filepath.Join(home, ".zsh_history"),
		filepath.Join(home, ".bash_history"),
		filepath.Join(home, ".local", "share", "fish", "fish_history"),
	}
	if h := os.Getenv("HISTFILE"); h != "" && filepath.IsAbs(h) {
		files = append(files, h)
	}
	return files
}

// shellActiveSince returns the latest modification time of the shell
// history files after since, if any changed: the student ran a command.
func shellActiveSince(since time.Time) (time.Time, bool) {
	var latest time.Time
	for _, f := range shellHistoryFiles() {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		if m := fi.ModTime(); m.After(since) && m.After(latest) {
			latest = m
		}
	}
	return latest, !latest.IsZero()
}
