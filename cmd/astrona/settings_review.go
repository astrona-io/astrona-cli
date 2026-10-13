package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"astrona/internal/terms"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type reviewedSetting struct {
	Revision       int    `json:"revision"`
	Default        string `json:"default"`
	Description    string `json:"description"`
	ExplicitChoice bool   `json:"explicitChoice"`
}

type settingsReceipt struct {
	Format      int                        `json:"format"`
	CLIVersion  string                     `json:"cliVersion"`
	ReviewedAt  time.Time                  `json:"reviewedAt"`
	Definitions map[string]reviewedSetting `json:"definitions"`
}

func reviewDefinitions() map[string]reviewedSetting {
	defs := make(map[string]reviewedSetting)
	for _, d := range settingsDefinitions() {
		r := reviewedSetting{Revision: 1, Default: d.defaultValue, Description: d.description}
		if d.key == "update-check" {
			r.Description += ". Contacts GitHub for release information; GitHub receives your IP address. Disabled in CI and non-terminal sessions."
		}
		defs[d.key] = r
	}
	return defs
}

func settingsReceiptPath(path string) string {
	return filepath.Join(filepath.Dir(path), "settings-review.json")
}

func loadSettingsReceipt(path string) (settingsReceipt, error) {
	var receipt settingsReceipt
	data, err := readSettingsFile(settingsReceiptPath(path))
	if err != nil || data == nil {
		return receipt, err
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Format != 1 || receipt.ReviewedAt.IsZero() || receipt.Definitions == nil {
		return receipt, fmt.Errorf("invalid settings review in %s; repair or remove that receipt, then run astrona settings review", settingsReceiptPath(path))
	}
	return receipt, nil
}

func changedSettings(previous, current map[string]reviewedSetting) []string {
	var keys []string
	for key, def := range current {
		if old, ok := previous[key]; !ok || old != def {
			keys = append(keys, key)
		}
	}
	for key := range previous {
		if _, ok := current[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func pendingSettings(previous, current map[string]reviewedSetting, saved userSettings) []string {
	changes := changedSettings(previous, current)
	for key, def := range current {
		_, explicit := saved[key]
		old, known := previous[key]
		if def.ExplicitChoice && !explicit && known && old == def {
			changes = append(changes, key)
		}
	}
	sort.Strings(changes)
	return changes
}

func settingsReviewReason(old, current reviewedSetting, known bool) string {
	if !known {
		return "This setting has not been reviewed before. Choose how it should work on your machine."
	}
	var reasons []string
	if old.Default != current.Default {
		reasons = append(reasons, fmt.Sprintf("The built-in default changed from %s to %s. Saved values are not replaced by the new default.", settingValueText(old.Default), settingValueText(current.Default)))
	}
	if old.Description != current.Description {
		reasons = append(reasons, "The setting's explanation changed. Previous explanation: "+sanitizeLine(old.Description))
	}
	if old.Revision != current.Revision {
		reasons = append(reasons, fmt.Sprintf("The behavior revision changed from %d to %d. Review the current explanation before continuing.", old.Revision, current.Revision))
	}
	if old.ExplicitChoice != current.ExplicitChoice {
		reasons = append(reasons, "The requirement for an explicit choice changed.")
	}
	if len(reasons) == 0 {
		return "This setting requires an explicit saved choice, but none is saved."
	}
	return strings.Join(reasons, " ")
}

func loadReviewValues(path string, previous, current map[string]reviewedSetting) (userSettings, error) {
	retired := make(map[string]bool)
	for key := range previous {
		if _, exists := current[key]; !exists {
			retired[key] = true
		}
	}
	return loadSettingsWithRetired(path, retired)
}

// The ID binds automation approval to both the definitions and saved choices.
// A release number alone cannot identify a settings change in a developer build.
func settingsReviewID(defs map[string]reviewedSetting, saved userSettings) (string, error) {
	data, err := json.Marshal(struct {
		Definitions map[string]reviewedSetting `json:"definitions"`
		Saved       userSettings               `json:"saved"`
	}{defs, saved})
	if err != nil {
		return "", fmt.Errorf("encode settings review: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func settingsReviewExempt(cmd *cobra.Command) bool {
	return cmd.Name() == "terms" || termsExempt(cmd)
}

func settingsInteractive(cmd *cobra.Command) bool {
	in, inOK := cmd.InOrStdin().(*os.File)
	out, outOK := cmd.ErrOrStderr().(*os.File)
	return !runTestMode(cmd) && inOK && outOK && isatty.IsTerminal(in.Fd()) && isatty.IsTerminal(out.Fd()) && !terms.InCI(os.Getenv)
}

func checkSettingsReview(cmd *cobra.Command, flags *rootFlags) error {
	if settingsReviewExempt(cmd) {
		return nil
	}
	path, err := settingsPath(flags)
	if err != nil {
		return err
	}
	return ensureSettingsReview(cmd, path, reviewDefinitions(), settingsInteractive(cmd), "")
}

func ensureSettingsReview(cmd *cobra.Command, path string, defs map[string]reviewedSetting, interactive bool, accept string) error {
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	receipt, err := loadSettingsReceipt(path)
	if err != nil {
		return err
	}
	saved, err := loadReviewValues(path, receipt.Definitions, defs)
	if err != nil {
		return err
	}
	if len(pendingSettings(receipt.Definitions, defs, saved)) == 0 && accept == "" {
		return nil
	}
	if !interactive && accept == "" {
		return fmt.Errorf("CLI settings need review before this command can run; use astrona settings view, then astrona settings review (for CI: settings review --accept <review-id>)")
	}
	return withSettingsLock(path, func() error {
		// Re-read under the same lock used by settings set/unset. A concurrent
		// review may have finished while this process was waiting for input.
		receipt, err := loadSettingsReceipt(path)
		if err != nil {
			return err
		}
		saved, err := loadReviewValues(path, receipt.Definitions, defs)
		if err != nil {
			return err
		}
		changed := pendingSettings(receipt.Definitions, defs, saved)
		if accept != "" {
			id, err := settingsReviewID(defs, saved)
			if err != nil {
				return err
			}
			if accept != id {
				return fmt.Errorf("settings review ID does not match; run astrona settings view again after any settings edit")
			}
		}
		if len(changed) == 0 {
			return nil
		}
		out := cmd.ErrOrStderr()
		if _, err := fmt.Fprintf(out, "Review CLI settings (%s). Saved choices are kept unless you change them.\n", Version); err != nil {
			return err
		}
		for _, key := range changed {
			def, exists := defs[key]
			old, known := receipt.Definitions[key]
			if !exists {
				if _, err := fmt.Fprintf(out, "\n%s was removed (previous default: %s).\n  Why: This CLI no longer supports this setting. Its saved override will be removed only if you approve this review.\n", key, settingValueText(old.Default)); err != nil {
					return err
				}
				delete(saved, key)
				continue
			}
			value := def.Default
			if stored, ok := saved[key]; ok {
				value = string(stored)
			}
			previous := "new setting"
			if known {
				previous = "previous default: " + settingValueText(old.Default)
			}
			if _, err := fmt.Fprintf(out, "\n%s (%s)\n  %s\n  Default: %s; current choice: %s\n", key, previous, def.Description, settingValueText(def.Default), settingValueText(value)); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "  Why review: %s\n", settingsReviewReason(old, def, known)); err != nil {
				return err
			}
			currentValue := value
			if accept != "" {
				if _, explicit := saved[key]; def.ExplicitChoice && !explicit {
					return fmt.Errorf("%s needs an explicit choice: astrona settings set %s <value>; then obtain a new review ID", key, key)
				}
			} else {
				prompt := fmt.Sprintf("  Value [%s] (enter to keep, q to cancel): ", settingValueText(value))
				if def.Default == "true" || def.Default == "false" {
					prompt = fmt.Sprintf("  Value [%s] (yes/no, enter to keep, q to cancel): ", settingValueText(value))
				}
				if def.ExplicitChoice {
					prompt = "  Type your value explicitly (q to cancel): "
					if def.Default == "true" || def.Default == "false" {
						prompt = "  Type your value explicitly (yes/no, q to cancel): "
					}
				}
				for {
					if _, err := fmt.Fprint(out, prompt); err != nil {
						return err
					}
					answer, err := readSettingsAnswer(cmd.Context(), cmd.InOrStdin())
					if err != nil {
						return err
					}
					if answer == "q" {
						return fmt.Errorf("settings review cancelled; no choices saved")
					}
					if answer != "" || def.ExplicitChoice {
						if _, err := parseSetting(key, answer); err != nil {
							if _, err := fmt.Fprintf(out, "  %s. Please try again, or type q to cancel.\n", err); err != nil {
								return err
							}
							continue
						}
					}
					if answer != "" {
						value = answer
					}
					break
				}
			}
			parsed, err := parseSetting(key, value)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "  Proposed: %s -> %s (saved only after approval)\n", settingValueText(currentValue), settingValueText(string(parsed))); err != nil {
				return err
			}
			saved[key] = parsed
		}
		if accept == "" {
			if _, err := fmt.Fprint(out, "\nSave these choices and continue? [y/N] "); err != nil {
				return err
			}
			answer, err := readSettingsAnswer(cmd.Context(), cmd.InOrStdin())
			if err != nil {
				return err
			}
			if answer != "y" && answer != "yes" {
				return fmt.Errorf("settings review cancelled; no choices saved")
			}
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if err := writeSettingsFile(path, saved); err != nil {
			return err
		}
		// Write acknowledgement last. If interrupted between the two atomic
		// replacements, choices survive but the next command asks again.
		receipt = settingsReceipt{1, Version, time.Now().UTC(), defs}
		if err := writeSettingsFile(settingsReceiptPath(path), receipt); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Settings reviewed. Change a choice with astrona settings set <key> <value>.")
		return err
	})
}

// Read one bounded line without consuming input for the next command prompt.
func readSettingsAnswer(ctx context.Context, in io.Reader) (string, error) {
	var answer strings.Builder
	var b [1]byte
	for answer.Len() < 128 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := io.ReadFull(in, b[:]); err != nil {
			return "", fmt.Errorf("settings review interrupted; no choices saved: %w", err)
		}
		if b[0] == '\n' {
			return strings.ToLower(strings.TrimSpace(answer.String())), nil
		}
		answer.WriteByte(b[0])
	}
	return "", fmt.Errorf("settings answer is too long; no choices saved")
}
