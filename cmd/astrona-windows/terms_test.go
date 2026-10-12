package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSetupTermsBeforeWSL(t *testing.T) {
	for _, args := range [][]string{{"--yes"}, {"--accept-terms"}, {"--accept-terms=true"}, {"--accept-terms=false"}, {"--help"}, {"--terms"}, {"--invalid"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := &recordingWSL{fakeWSL: fakeWSL{outputs: readyOutputs()}}
			want := errors.New("declined")
			called := false
			err := setupWithTerms(f, strings.NewReader(""), io.Discard, args, func(explicit bool) error {
				called = true
				if explicit != (args[0] == "--accept-terms" || args[0] == "--accept-terms=true") {
					t.Fatalf("explicit=%v", explicit)
				}
				return want
			})
			skip := args[0] == "--help" || args[0] == "--terms" || args[0] == "--invalid"
			if called == skip || (!skip && !errors.Is(err, want)) {
				t.Fatalf("called=%v err=%v", called, err)
			}
			if len(f.calls) != 0 || len(f.attached) != 0 {
				t.Fatal("WSL touched before consent")
			}
		})
	}
}

func TestSetupForwardsExplicitTerms(t *testing.T) {
	f := &fakeWSL{outputs: readyOutputs()}
	old := installSelfFn
	installSelfFn = func(io.Writer) error { return nil }
	t.Cleanup(func() { installSelfFn = old })
	if err := setupWithTerms(f, strings.NewReader(""), io.Discard, []string{"--yes", "--accept-terms"}, func(explicit bool) error {
		if !explicit {
			t.Fatal("explicit acceptance lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.attached) != 1 || !strings.HasSuffix(strings.Join(f.attached[0], " "), "setup --accept-terms --yes") {
		t.Fatalf("inner setup=%v", f.attached)
	}
}

func TestCIEnvironmentReachesWSL(t *testing.T) {
	for _, original := range []string{"", "KEEP/p:CI/u:ALSO_KEEP/l", "CI/w"} {
		environ := []string{"Path=tools", "CI=false", "WSLENV=" + original}
		got := ciWSLEnv(environ, true)
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "CI=true\nWSLENV=") || strings.Contains(joined, "CI=false") || !strings.HasSuffix(joined, "CI") {
			t.Fatalf("env=%v", got)
		}
		if original == "KEEP/p:CI/u:ALSO_KEEP/l" && !strings.Contains(joined, "KEEP/p:ALSO_KEEP/l:CI") {
			t.Fatal("lost unrelated WSLENV entries")
		}
		if strings.Join(ciWSLEnv(environ, false), "\n") != strings.Join(environ, "\n") {
			t.Fatal("changed non-CI environment")
		}
	}
}
