package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func reviewCommand(input string) (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader(input))
	var out bytes.Buffer
	cmd.SetErr(&out)
	return cmd, &out
}

func initialReviewInput() string {
	defs := reviewDefinitions()
	keys := make([]string, 0, len(defs))
	for key := range defs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var input strings.Builder
	for _, key := range keys {
		switch key {
		case "parallel":
			input.WriteString("2")
		case "update-check":
			input.WriteString("false")
		}
		input.WriteByte('\n')
	}
	return input.String() + "y\n"
}

func TestPinnedCISettingsReview(t *testing.T) {
	saved := userSettings{"verbose": json.RawMessage("false"), "keep-context": json.RawMessage("false"), "parallel": json.RawMessage("1"), "update-check": json.RawMessage("false")}
	saved["cache-retention-days"] = json.RawMessage("30")
	saved["cache-max-size-gb"] = json.RawMessage("50")
	id, err := settingsReviewID(reviewDefinitions(), saved)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile("../../.github/scripts/review-test-settings.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(helper), "--accept '"+id+"'") {
		t.Fatalf("Review the CI settings choices, then update the pinned ID in review-test-settings.sh to %s", id)
	}
}

func acceptReview(t *testing.T, path string, defs map[string]reviewedSetting) {
	t.Helper()
	value := "false"
	if err := changeSetting(path, "update-check", &value); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := settingsReviewID(defs, saved)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := reviewCommand("")
	if err := ensureSettingsReview(cmd, path, defs, false, id); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsViewReadOnly(t *testing.T) {
	for _, args := range [][]string{{"settings", "view"}, {"settings", "view", "-o", "json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s, provider := dataTestStore(t)
			path := filepath.Join(s.Data, "settings.json")
			flags := &rootFlags{settingsFile: path}
			root := newRootCmd(flags)
			installCommandGate(root, func(c *cobra.Command) error { return checkSettingsReview(c, flags) })
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			if err := executeWithDataLock(root, provider); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.Data); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("view created files: %v", err)
			}
			if len(args) == 2 {
				for _, want := range []string{"Default:", "Off (false)", "GitHub", "Needs review:", "Review ID:"} {
					if !strings.Contains(out.String(), want) {
						t.Fatalf("missing %s: %s", want, out.String())
					}
				}
			} else {
				var v settingsView
				if err := json.Unmarshal(out.Bytes(), &v); err != nil || len(v.ID) != 64 || len(v.Pending) != len(settingsDefinitions()) {
					t.Fatalf("view = %s; err = %v", out.String(), err)
				}
			}
		})
	}
}

func TestReviewInteractiveAndUnchangedRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	defs := reviewDefinitions()
	cmd, out := reviewCommand(initialReviewInput() + "next prompt\n")
	if err := ensureSettingsReview(cmd, path, defs, true, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Default: true") || !strings.Contains(out.String(), "Type your value explicitly") {
		t.Fatal(out.String())
	}
	rest, err := io.ReadAll(cmd.InOrStdin())
	if err != nil || string(rest) != "next prompt\n" {
		t.Fatalf("consumed later input: %q, %v", rest, err)
	}
	saved, err := loadSettings(path)
	if err != nil || string(saved["parallel"]) != "2" || string(saved["update-check"]) != "false" {
		t.Fatalf("choices = %v, %v", saved, err)
	}
	receipt, err := loadSettingsReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CLIVersion = "another-release"
	if err := writeSettingsFile(settingsReceiptPath(path), receipt); err != nil {
		t.Fatal(err)
	}
	cmd, out = reviewCommand("")
	if err := ensureSettingsReview(cmd, path, defs, false, ""); err != nil || out.Len() != 0 {
		t.Fatalf("unchanged release prompted: %s, %v", out.String(), err)
	}
	for _, file := range []string{path, settingsReceiptPath(path)} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("permissions on %s: %v, %v", file, info, err)
		}
	}
}

func TestReviewDeclineAndInvalidInputDoNotSave(t *testing.T) {
	for _, input := range []string{"q\n", "", "\n99\n", "\n\n\n", "\n\nfalse\n\nn\n", "\n\nfalse\n\ny", strings.Repeat("x", 129)} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			value := "3"
			if err := changeSetting(path, "parallel", &value); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cmd, _ := reviewCommand(input)
			if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); err == nil {
				t.Fatal("accepted incomplete/invalid review")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("changed preferences: %q, %v", after, err)
			}
			if _, err := os.Stat(settingsReceiptPath(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrote receipt")
			}
		})
	}
}

func TestReviewExplicitChoiceRetriesKeepEarlierAnswers(t *testing.T) {
	for _, tc := range []struct{ name, answer, want string }{
		{"blank then false", "\nfalse\n", "false"},
		{"blank then true", "\ntrue\n", "true"},
		{"whitespace and invalid", " \t\r\nyes\n1\nfalse\n", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			input := strings.Replace(initialReviewInput(), "false\n", tc.answer, 1) + "next prompt\n"
			cmd, out := reviewCommand(input)
			if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); err != nil {
				t.Fatalf("review failed: %v\n%s", err, out.String())
			}
			saved, err := loadSettings(path)
			if err != nil || string(saved["parallel"]) != "2" || string(saved["update-check"]) != tc.want {
				t.Fatalf("lost choices: %v, %v", saved, err)
			}
			if strings.Count(out.String(), "parallel (new setting)") != 1 || !strings.Contains(out.String(), "update-check must be true or false. Please try again") {
				t.Fatalf("did not retry only the current question: %s", out.String())
			}
			if strings.Count(out.String(), "Type your value explicitly (true/false, q to cancel):") != strings.Count(tc.answer, "\n") {
				t.Fatalf("unexpected number of prompts: %s", out.String())
			}
			rest, err := io.ReadAll(cmd.InOrStdin())
			if err != nil || string(rest) != "next prompt\n" {
				t.Fatalf("consumed later input: %q, %v", rest, err)
			}
			receipt, err := loadSettingsReceipt(path)
			if err != nil || len(pendingSettings(receipt.Definitions, reviewDefinitions(), saved)) != 0 {
				t.Fatalf("review not recorded: %v", err)
			}
		})
	}
}

func TestReviewExplicitChoiceRetryCanStillCancel(t *testing.T) {
	for _, answer := range []string{"\nq\n", "\n", "\ntrue"} {
		t.Run(answer, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			value := "3"
			if err := changeSetting(path, "parallel", &value); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			prefix, _, found := strings.Cut(initialReviewInput(), "false\n")
			if !found {
				t.Fatal("missing explicit choice in test input")
			}
			cmd, out := reviewCommand(prefix + answer)
			if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); err == nil {
				t.Fatal("blank input approved the default")
			}
			if !strings.Contains(out.String(), "Please try again") {
				t.Fatal("did not retry the blank answer")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("cancel saved earlier answers: %q, %v", after, err)
			}
			if _, err := os.Stat(settingsReceiptPath(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cancel saved a review receipt")
			}
		})
	}
}

func TestReviewDetectsSchemaChangesAndPreservesChoices(t *testing.T) {
	for _, kind := range []string{"default", "description", "revision", "added", "removed", "privacy"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			defs := reviewDefinitions()
			acceptReview(t, path, defs)
			receipt, err := loadSettingsReceipt(path)
			if err != nil {
				t.Fatal(err)
			}
			key := "parallel"
			switch kind {
			case "default":
				d := defs[key]
				d.Default = "2"
				defs[key] = d
			case "description":
				d := defs[key]
				d.Description += "; changed behavior"
				defs[key] = d
			case "revision":
				d := defs[key]
				d.Revision++
				defs[key] = d
			case "added":
				delete(receipt.Definitions, key)
			case "removed":
				key = "retired"
				receipt.Definitions[key] = reviewedSetting{Default: "false"}
				saved, err := loadSettings(path)
				if err != nil {
					t.Fatal(err)
				}
				saved[key] = json.RawMessage("true")
				if err := writeSettingsFile(path, saved); err != nil {
					t.Fatal(err)
				}
			case "privacy":
				d := defs[key]
				d.ExplicitChoice = true
				defs[key] = d
			}
			if err := writeSettingsFile(settingsReceiptPath(path), receipt); err != nil {
				t.Fatal(err)
			}
			cmd, _ := reviewCommand("")
			if err := ensureSettingsReview(cmd, path, defs, false, ""); err == nil {
				t.Fatal("missed upgrade change")
			}
			input := "\ny\n"
			switch kind {
			case "removed":
				input = "y\n"
			case "privacy":
				input = "1\ny\n"
			}
			cmd, out := reviewCommand(input)
			if err := ensureSettingsReview(cmd, path, defs, true, ""); err != nil {
				t.Fatalf("review: %s, %v", out.String(), err)
			}
			saved, err := loadSettings(path)
			if err != nil || string(saved["parallel"]) != "1" || string(saved["update-check"]) != "false" {
				t.Fatalf("lost choices: %v, %v", saved, err)
			}
			if _, exists := saved["retired"]; exists {
				t.Fatal("removed setting not migrated")
			}
		})
	}
}

func TestReviewAutomationAndStaleID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	defs := reviewDefinitions()
	view, err := buildSettingsView(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := reviewCommand("")
	if err := ensureSettingsReview(cmd, path, defs, false, view.ID); err == nil || !strings.Contains(err.Error(), "explicit choice") {
		t.Fatalf("approved sensitive default: %v", err)
	}
	value := "false"
	if err := changeSetting(path, "update-check", &value); err != nil {
		t.Fatal(err)
	}
	if err := ensureSettingsReview(cmd, path, defs, false, view.ID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("accepted stale ID: %v", err)
	}
	view, err = buildSettingsView(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeSettings(t, path, "review", "--accept", view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSettings(t, path, "review", "--accept="); err == nil {
		t.Fatal("accepted empty approval flag")
	}
	if err := changeSetting(path, "update-check", nil); err != nil {
		t.Fatal(err)
	}
	if err := ensureSettingsReview(cmd, path, defs, false, ""); err == nil {
		t.Fatal("missing explicit network choice did not prompt")
	}
}

func TestReviewGatePrecedesAllOperationalHooks(t *testing.T) {
	for _, args := range [][]string{{"run", "--yes", "--trust", "--accept-terms"}, {"res", "run", "test"}, {"setup", "--yes"}, {}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			flags := &rootFlags{settingsFile: path}
			root := newRootCmd(flags)
			var stub func(*cobra.Command)
			stub = func(c *cobra.Command) {
				c.Args = cobra.ArbitraryArgs
				c.PersistentPreRunE = func(*cobra.Command, []string) error { t.Fatal("setup before review"); return nil }
				c.RunE = func(*cobra.Command, []string) error { t.Fatal("operation before review"); return nil }
				for _, child := range c.Commands() {
					stub(child)
				}
			}
			stub(root)
			installCommandGate(root, func(c *cobra.Command) error { return checkSettingsReview(c, flags) })
			installTermsGate(root, flags, func(*cobra.Command, bool) error { return nil })
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "settings need review") {
				t.Fatalf("gate failed: %v", err)
			}
		})
	}
}

func TestReviewExemptionsAndCancellation(t *testing.T) {
	root := newRootCmd(&rootFlags{})
	for _, path := range [][]string{{"settings", "view"}, {"settings", "set"}, {"terms"}, {"data", "reset"}, {"destroy"}, {"stop"}, {"logout"}, {"port-forward", "stop"}, {"docgen"}, {"playground-watchdog"}} {
		cmd, _, err := root.Find(path)
		if err != nil || !settingsReviewExempt(cmd) {
			t.Fatalf("not exempt: %v, %v", path, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd, _ := reviewCommand("y\n")
	cmd.SetContext(ctx)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := readSettingsAnswer(ctx, strings.NewReader("y\n")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHumanSettingsValues(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"verbose", "true", "On (true)"}, {"verbose", "false", "Off (false)"},
		{"cache-retention-days", "30", "30 days"}, {"cache-max-size-gb", "50", "50 GB"},
		{"cache-max-size-gb", "0", "Unlimited (0 GB)"}, {"parallel", "2", "2 concurrent cluster(s)"},
		{"future", "42", "42"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			if got := humanSetting(tc.key, tc.value); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReviewUnsafeOrCorruptReceipt(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "corrupt", "null", "oversized", "lock"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			receipt := settingsReceiptPath(path)
			switch kind {
			case "symlink":
				if err := os.Symlink(path, receipt); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(receipt, 0o700); err != nil {
					t.Fatal(err)
				}
			case "corrupt", "null", "oversized":
				data := kind
				if kind == "oversized" {
					data = strings.Repeat("x", 65537)
				}
				if err := os.WriteFile(receipt, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := withSettingsLock(path, func() error {
					cmd, _ := reviewCommand("\n\nfalse\n\ny\n")
					if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); err == nil {
						t.Fatal("concurrent writer accepted")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return
			}
			cmd, _ := reviewCommand("\n\nfalse\n\ny\n")
			if err := ensureSettingsReview(cmd, path, reviewDefinitions(), true, ""); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}
