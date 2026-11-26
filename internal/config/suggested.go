package config

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Bounds of metadata.suggestedCommands.
const (
	MaxSuggestedCommands     = 8
	MaxSuggestedCommandRunes = 200
	MaxSuggestedLabelRunes   = 80
)

// SuggestedCommand is one entry of metadata.suggestedCommands: a command a
// playground's browser workspace offers to copy into the terminal, with an
// optional label. In YAML an entry is either the command as a plain string
// or a mapping {command, label}.
type SuggestedCommand struct {
	Command string `yaml:"command" json:"command"`
	Label   string `yaml:"label" json:"label"`
}

// UnmarshalYAML reads either form. An unknown key in the mapping form is an
// error, since the strict decode that finds typos elsewhere cannot see
// into a custom unmarshaller.
func (s *SuggestedCommand) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var c string
		if err := n.Decode(&c); err != nil {
			return err
		}
		*s = SuggestedCommand{Command: c}
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i].Value; k != "command" && k != "label" {
				return fmt.Errorf("line %d: metadata.suggestedCommands: unknown field %q (an entry has command and label)", n.Content[i].Line, k)
			}
		}
		var fields struct {
			Command string `yaml:"command"`
			Label   string `yaml:"label"`
		}
		if err := n.Decode(&fields); err != nil {
			return err
		}
		*s = SuggestedCommand{Command: fields.Command, Label: fields.Label}
		return nil
	}
	return fmt.Errorf("line %d: metadata.suggestedCommands: an entry is a command string or {command, label}", n.Line)
}

// ValidateSuggestedCommands checks metadata.suggestedCommands: at most
// MaxSuggestedCommands entries, each a non-empty single-line command of at
// most MaxSuggestedCommandRunes characters with an optional single-line
// label.
func ValidateSuggestedCommands(cfg *LabConfig) error {
	list := cfg.Metadata.SuggestedCommands
	if len(list) > MaxSuggestedCommands {
		return fmt.Errorf("metadata.suggestedCommands: at most %d entries, got %d", MaxSuggestedCommands, len(list))
	}
	for i, c := range list {
		if err := CheckSuggestedCommand(c.Command, c.Label); err != nil {
			return fmt.Errorf("metadata.suggestedCommands[%d]: %w", i, err)
		}
	}
	return nil
}

// CheckSuggestedCommand checks one suggested command and its label.
func CheckSuggestedCommand(command, label string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is empty")
	}
	if err := singleLine(command, MaxSuggestedCommandRunes); err != nil {
		return fmt.Errorf("command %w", err)
	}
	if err := singleLine(label, MaxSuggestedLabelRunes); err != nil {
		return fmt.Errorf("label %w", err)
	}
	return nil
}

// singleLine rejects text with a line break or other control character, or
// longer than limit characters.
func singleLine(s string, limit int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n > limit {
		return fmt.Errorf("is %d characters long, at most %d", n, limit)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("must be a single line without control characters")
		}
	}
	return nil
}
