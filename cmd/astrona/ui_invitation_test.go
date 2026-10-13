package main

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestRunUIInvitation(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		wantUI       bool
	}{
		{"enter", "\n", true},
		{"yes", "yes\n", true},
		{"short yes", "Y\n", true},
		{"no", "no\n", false},
		{"short no", " N \r\n", false},
		{"retry", "maybe\nyes\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--git", "https://example.invalid/labs", "run", "--", "-lab"}
			in := strings.NewReader(tc.answer + "next prompt\n")
			var out bytes.Buffer
			got, err := offerRunUI(args, in, &out, true)
			if err != nil || wantsUIPane(got) != tc.wantUI {
				t.Fatalf("UI choice: %v, %v", got, err)
			}
			if !tc.wantUI && !reflect.DeepEqual(got, args) {
				t.Fatal("declining changed command arguments")
			}
			if !strings.Contains(out.String(), "v0.5.0") || !strings.Contains(out.String(), "yes/no, Enter for yes") {
				t.Fatal("missing default or version explanation")
			}
			remaining, _ := io.ReadAll(in)
			if string(remaining) != "next prompt\n" {
				t.Fatalf("consumed the next answer: %q", remaining)
			}
			root := newRootCmd(&rootFlags{})
			cmd, rest, err := root.Find(got)
			if err != nil || cmd.ParseFlags(rest) != nil || !reflect.DeepEqual(cmd.Flags().Args(), []string{"-lab"}) {
				t.Fatalf("lost literal lab argument: %v", got)
			}
			if value, _ := cmd.Flags().GetString("git"); value != "https://example.invalid/labs" {
				t.Fatal("lost inherited flag")
			}
		})
	}
}

func TestRunUIInvitationSkipsExplicitAndNoninteractiveCommands(t *testing.T) {
	for _, args := range [][]string{
		{"run", "lab", "--ui"}, {"run", "lab", "--ui=false"},
		{"run", "lab", "--headless"}, {"test", "./lab"},
		{"run", "lab", "--ui-startup"}, {"run", "lab", "--yes"}, {"run", "lab", "-y"},
		{"run", "lab", "--help"}, {"help", "run"}, {"run", "lab", "--unknown"},
		{"run", "one", "two"}, {"run", "renew"}, {"ui"}, {"list"}, {},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			got, err := offerRunUI(args, nil, &out, true)
			if err != nil || out.Len() != 0 || !reflect.DeepEqual(got, args) {
				t.Fatalf("unexpected invitation for %v: %v", args, err)
			}
		})
	}
	var out bytes.Buffer
	args := []string{"run", "lab"}
	got, err := offerRunUI(args, nil, &out, false)
	if err != nil || out.Len() != 0 || !reflect.DeepEqual(got, args) {
		t.Fatal("noninteractive invocation prompted")
	}
}

func TestRunUIInvitationRequiresAnAnswer(t *testing.T) {
	for _, input := range []string{"", "yes", strings.Repeat("y", 128)} {
		args := []string{"run", "lab"}
		got, err := offerRunUI(args, strings.NewReader(input), io.Discard, true)
		if err == nil || !reflect.DeepEqual(got, args) || wantsUIPane(got) {
			t.Fatal("interrupted input accepted the default")
		}
		if len(input) < 128 && !errors.Is(err, io.EOF) {
			t.Fatalf("lost input error: %v", err)
		}
	}
}
