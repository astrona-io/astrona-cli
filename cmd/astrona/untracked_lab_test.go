package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestApproveUntrackedLab(t *testing.T) {
	for _, tc := range []struct {
		name, answer, lab                               string
		tty, optIn, signedIn, approved, wantErr, prompt bool
	}{
		{name: "yes", answer: "yes\n", lab: "ATS000/lab", tty: true, approved: true, prompt: true},
		{name: "uppercase yes", answer: "Y\n", lab: "ATS000/lab", tty: true, approved: true, prompt: true},
		{name: "no", answer: "no\n", lab: "ATS000/lab", tty: true, prompt: true},
		{name: "default no", answer: "\n", lab: "ATS000/lab", tty: true, prompt: true},
		{name: "EOF", lab: "ATS000/lab", tty: true, prompt: true},
		{name: "headless requires opt-in", lab: "ATS000/lab", wantErr: true},
		{name: "headless explicit opt-in", lab: "ATS000/lab", optIn: true, approved: true},
		{name: "signed in", lab: "ATS000/lab", signedIn: true, approved: true},
		{name: "local lab", approved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var acct *labAccount
			if tc.signedIn {
				acct = &labAccount{}
			}
			approved, err := approveUntrackedLab(strings.NewReader(tc.answer), &out, tc.tty, tc.optIn, tc.lab, acct)
			if approved != tc.approved || (err != nil) != tc.wantErr {
				t.Fatalf("approved=%v, err=%v", approved, err)
			}
			if strings.Contains(out.String(), "[y/N]") != tc.prompt {
				t.Errorf("unexpected prompt: %q", out.String())
			}
			if tc.lab != "" && !tc.signedIn {
				for _, want := range []string{"progress and results won't be tracked", "astrona login"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in %q", want, out.String())
					}
				}
			} else if out.Len() != 0 {
				t.Errorf("unnecessary warning: %q", out.String())
			}
			if err != nil && !strings.Contains(err.Error(), "--without-login") {
				t.Errorf("error lacks automation flag: %v", err)
			}
		})
	}
}
