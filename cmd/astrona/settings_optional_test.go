package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise main's actual command gates without building a second CLI binary.
func TestSettingsOptionalProcess(t *testing.T) {
	if os.Getenv("ASTRONA_SETTINGS_OPTIONAL_TEST") != "1" {
		return
	}
	os.Args = []string{"astrona", "progress", "--headless", "--accept-terms", "-o", "json"}
	main()
	os.Exit(0)
}

func TestCommandsRunWithoutSettingsReview(t *testing.T) {
	for _, receipt := range []string{"missing", "outdated", "invalid"} {
		t.Run(receipt, func(t *testing.T) {
			home := t.TempDir()
			data := filepath.Join(home, ".astrona-dev")
			if err := os.MkdirAll(data, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(data, "settings-review.json")
			var before []byte
			if receipt != "missing" {
				before = []byte("broken receipt")
				if receipt == "outdated" {
					before = []byte(`{"format":1,"cliVersion":"old","reviewedAt":"2020-01-01T00:00:00Z","definitions":{}}`)
				}
				if err := os.WriteFile(path, before, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Repeated commands must neither ask for review nor write approval.
			for range 2 {
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSettingsOptionalProcess$")
				cmd.Env = append(os.Environ(), "ASTRONA_SETTINGS_OPTIONAL_TEST=1", "HOME="+home, "CI=true", "ASTRONA_NO_UPDATE_CHECK=1")
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) != "[]" || strings.Contains(strings.ToLower(stderr.String()), "settings") {
					t.Fatalf("command: %v; stdout=%q stderr=%q", err, out.String(), stderr.String())
				}
			}
			after, err := os.ReadFile(path)
			if receipt == "missing" {
				if !os.IsNotExist(err) {
					t.Fatalf("command created review receipt: %v", err)
				}
			} else if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("command changed review receipt: %v", err)
			}
		})
	}
}
