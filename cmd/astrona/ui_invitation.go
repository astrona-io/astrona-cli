package main

import (
	"fmt"
	"io"
	"strings"
)

// Offer before command gates so no downloads or setup precede the UI choice.
func offerRunUI(args []string, in io.Reader, out io.Writer, interactive bool) ([]string, error) {
	if !interactive {
		return args, nil
	}
	root := newRootCmd(&rootFlags{})
	cmd, rest, err := root.Find(args)
	if err != nil || cmd.Name() != "run" || cmd.Parent() != root {
		return args, nil
	}
	cmd.InitDefaultHelpFlag()
	if err := cmd.ParseFlags(rest); err != nil {
		return args, nil
	}
	help, _ := cmd.Flags().GetBool("help")
	yes, _ := cmd.Flags().GetBool("yes")
	if help || yes || cmd.Flags().Changed("ui") || cmd.Flags().Changed("ui-startup") || cmd.ValidateArgs(cmd.Flags().Args()) != nil {
		return args, nil
	}
	fmt.Fprintln(out, "Try Astrona's terminal UI to follow setup progress and work in terminal tabs.")
	fmt.Fprintln(out, "The UI will become the default in v0.5.0. It currently supports Kind labs.")
	for {
		fmt.Fprint(out, "Open the UI? (yes/no, Enter for yes): ")
		answer, err := readUIInvitationAnswer(in)
		if err != nil {
			return args, err
		}
		switch answer {
		case "", "yes", "y":
			return append([]string{"run", "--ui"}, rest...), nil
		case "no", "n":
			return args, nil
		default:
			fmt.Fprintln(out, "Please enter yes or no, or press Enter for yes.")
		}
	}
}

func readUIInvitationAnswer(in io.Reader) (string, error) {
	var answer strings.Builder
	var b [1]byte
	// Do not buffer input intended for the startup notice or the next prompt.
	for answer.Len() < 128 {
		if _, err := io.ReadFull(in, b[:]); err != nil {
			return "", fmt.Errorf("UI choice interrupted; no lab was started: %w", err)
		}
		if b[0] == '\n' {
			return strings.ToLower(strings.TrimSpace(answer.String())), nil
		}
		answer.WriteByte(b[0])
	}
	return "", fmt.Errorf("UI choice is too long; enter yes or no")
}
