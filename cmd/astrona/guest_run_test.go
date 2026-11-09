package main

import (
	"context"
	"encoding/json"
	"testing"

	"astrona/internal/settings"
	"github.com/spf13/cobra"
)

func TestGuestReminderLocalOnlySkipsAccountAndSettings(t *testing.T) {
	flags := &rootFlags{localOnly: true, settingsFile: "/nonexistent/settings.json"}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	acct, approved, err := confirmGuestLabRun(cmd, flags, nil, false)
	if err != nil || !approved || acct != nil {
		t.Fatalf("local-only: %v %v %v", acct, approved, err)
	}
}

func TestLoginReminderDefinitionIsBoolean(t *testing.T) {
	raw, err := settings.Parse("login-reminder", "no")
	if err != nil || string(raw) != "false" {
		t.Fatal("invalid boolean setting", err)
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil || enabled {
		t.Fatal("not stored as boolean")
	}
}
