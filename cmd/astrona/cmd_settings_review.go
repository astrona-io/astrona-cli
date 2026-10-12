package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type settingView struct {
	Key            string          `json:"key"`
	Value          json.RawMessage `json:"value"`
	Default        json.RawMessage `json:"default"`
	Source         string          `json:"source"`
	Description    string          `json:"description"`
	ExplicitChoice bool            `json:"explicitChoice"`
}

type settingsView struct {
	Path    string        `json:"path"`
	ID      string        `json:"reviewId"`
	Pending []string      `json:"pending"`
	Values  []settingView `json:"settings"`
}

func newSettingsViewCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use: "view", Short: "Show current settings, defaults, explanations and pending changes", Args: cobra.NoArgs,
		Long: "Show local settings before flags and environment-based suppression. This command is read-only. " +
			"The review ID identifies these definitions and saved values; it changes after edits. " +
			"It is not a tracking token or consent to send data.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(output, "text"); err != nil {
				return err
			}
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			view, err := buildSettingsView(path)
			if err != nil {
				return err
			}
			if output == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(view)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Astrona settings\nFile: %s\nValues shown before command flags and environment overrides.\n", path)
			for _, v := range view.Values {
				fmt.Fprintf(out, "\n%s\n  Current: %s (%s) | Default: %s\n  %s\n", v.Key, humanSetting(v.Key, string(v.Value)), v.Source, humanSetting(v.Key, string(v.Default)), v.Description)
				if v.ExplicitChoice {
					fmt.Fprintln(out, "  Requires an explicit choice during review.")
				}
			}
			if len(view.Pending) > 0 {
				fmt.Fprintf(out, "\nNeeds review: %s\nRun astrona settings review to choose values.\n", strings.Join(view.Pending, ", "))
			} else {
				fmt.Fprintln(out, "\nAll current settings have been reviewed.")
			}
			_, err = fmt.Fprintf(out, "Review ID: %s\nChange a value: astrona settings set <key> <value>\n", view.ID)
			return err
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "Output format: text or json (includes defaults, sources and review ID)")
	return cmd
}

func humanSetting(key, value string) string {
	switch value {
	case "true":
		return "On (true)"
	case "false":
		return "Off (false)"
	}
	switch key {
	case "cache-retention-days":
		return value + " days"
	case "cache-max-size-gb":
		if value == "0" {
			return "Unlimited (0 GB)"
		}
		return value + " GB"
	case "parallel":
		return value + " concurrent cluster(s)"
	}
	return value
}

func buildSettingsView(path string) (settingsView, error) {
	view := settingsView{Path: path, Pending: []string{}, Values: []settingView{}}
	defs := reviewDefinitions()
	receipt, err := loadSettingsReceipt(path)
	if err != nil {
		return view, err
	}
	saved, err := loadReviewValues(path, receipt.Definitions, defs)
	if err != nil {
		return view, err
	}
	view.ID, err = settingsReviewID(defs, saved)
	if err != nil {
		return view, err
	}
	view.Pending = append(view.Pending, pendingSettings(receipt.Definitions, defs, saved)...)
	for key, def := range defs {
		value, source := json.RawMessage(def.Default), "default"
		if stored, ok := saved[key]; ok {
			value, source = stored, "saved"
		}
		view.Values = append(view.Values, settingView{key, value, json.RawMessage(def.Default), source, def.Description, def.ExplicitChoice})
	}
	sort.Slice(view.Values, func(i, j int) bool { return view.Values[i].Key < view.Values[j].Key })
	return view, nil
}

func newSettingsReviewCmd(flags *rootFlags) *cobra.Command {
	var accept string
	cmd := &cobra.Command{
		Use: "review", Short: "Choose values for new or changed CLI settings", Args: cobra.NoArgs,
		Long: "Review new settings, changed defaults or descriptions, and removed settings. " +
			"Saved values are kept unless you change them. Without a terminal, set sensitive choices explicitly, " +
			"inspect settings view, then pass its exact --accept review ID. --yes and --accept-terms do not approve settings. " +
			"No tracking service is enabled by this command.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := settingsPath(flags)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("accept") && accept == "" {
				return fmt.Errorf("--accept needs the review ID from astrona settings view")
			}
			if err := ensureSettingsReview(cmd, path, reviewDefinitions(), settingsInteractive(cmd), accept); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "All current settings have been reviewed.")
			return err
		},
	}
	cmd.Flags().StringVar(&accept, "accept", "", "Acknowledge the exact review ID from settings view (for automation)")
	return cmd
}
