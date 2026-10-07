package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, r := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, r)
	}
	return b
}

func TestDecodeWSL(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{utf16le("Ubuntu\r\ndocker-desktop\r\n", false), "Ubuntu\r\ndocker-desktop\r\n"},
		{utf16le("Ubuntu\r\n", true), "Ubuntu\r\n"},
		{[]byte("27.3.1\n"), "27.3.1\n"},
	} {
		if got := decodeWSL(tc.in); got != tc.want {
			t.Errorf("decodeWSL(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDistroList(t *testing.T) {
	got := distroList("Ubuntu\r\n* docker-desktop\r\n\r\n")
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "docker-desktop" {
		t.Fatalf("distroList = %q", got)
	}
}

// The student's arguments are positional parameters, never part of the
// bash script — `astrona run "a; rm -rf ~"` must not run rm.
func TestForwardArgsNeverInterpolates(t *testing.T) {
	args := forwardArgs("Ubuntu", []string{"run", "a; rm -rf ~", "$(whoami)"})
	want := []string{"-d", "Ubuntu", "--", "bash", "-lc", `exec astrona "$@"`, "astrona", "run", "a; rm -rf ~", "$(whoami)"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forwardArgs = %q", args)
	}
}

func TestInstallInWSLPinsReleaseOnlyForCleanVersions(t *testing.T) {
	if s := installInWSL("v0.4.0"); !strings.Contains(s, "releases/download/v0.4.0/astrona-linux-") {
		t.Errorf("pinned release missing: %s", s)
	}
	for _, v := range []string{"dev", "v1; rm -rf ~", "v1$(id)"} {
		if s := installInWSL(v); !strings.Contains(s, "releases/latest/download/astrona-linux-") || strings.Contains(s, "rm -rf") || strings.Contains(s, "$(id)") {
			t.Errorf("version %q leaked into the script: %s", v, s)
		}
	}
}

// fakeWSL answers output() from a table (missing key = error) and records attach() calls.
type fakeWSL struct {
	outputs   map[string]string
	attached  [][]string
	attachErr error
}

func (f *fakeWSL) output(args ...string) (string, error) {
	if out, ok := f.outputs[strings.Join(args, " ")]; ok {
		return out, nil
	}
	return "", errors.New("exit status 1")
}

func (f *fakeWSL) attach(args ...string) error {
	f.attached = append(f.attached, args)
	return f.attachErr
}

func readyOutputs() map[string]string {
	return map[string]string{
		"--status":          "Default Version: 2",
		"-l -q":             "Ubuntu\r\n",
		"-d Ubuntu -- true": "",
		"-d Ubuntu -- docker info --format {{.ServerVersion}}": "27.3.1",
		"-d Ubuntu -- bash -lc command -v astrona":             "/home/s/.local/bin/astrona",
	}
}

func TestSetupInstallsWSLWhenMissing(t *testing.T) {
	f := &fakeWSL{outputs: map[string]string{}}
	var out bytes.Buffer
	if err := setup(f, strings.NewReader("y\n"), &out, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.attached) != 1 || strings.Join(f.attached[0], " ") != "--install --no-distribution" {
		t.Fatalf("attached = %q", f.attached)
	}
	if !strings.Contains(out.String(), "Restart Windows") {
		t.Errorf("no restart instruction: %s", out.String())
	}
}

func TestSetupDeclinedChangesNothing(t *testing.T) {
	f := &fakeWSL{outputs: map[string]string{}}
	if err := setup(f, strings.NewReader("n\n"), io.Discard, nil); err == nil || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q", err, f.attached)
	}
}

func TestSetupDryRunAttachesNothing(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu -- bash -lc command -v astrona")
	f := &fakeWSL{outputs: outs}
	if err := setup(f, strings.NewReader(""), io.Discard, []string{"--dry-run"}); err != nil || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q", err, f.attached)
	}
}

func TestSetupStopsAtDockerWithInstructions(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu -- docker info --format {{.ServerVersion}}")
	f := &fakeWSL{outputs: outs}
	var out bytes.Buffer
	err := setup(f, strings.NewReader(""), &out, nil)
	if err == nil || !strings.Contains(out.String(), "WSL integration") || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q out=%s", err, f.attached, out.String())
	}
}

func TestSetupFullRunInstallsAstronaRunsInnerSetupAndPath(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu -- bash -lc command -v astrona")
	f := &fakeWSL{outputs: outs}
	pathed := false
	old := installSelfFn
	installSelfFn = func(io.Writer) error { pathed = true; return nil }
	defer func() { installSelfFn = old }()

	if err := setup(f, strings.NewReader(""), io.Discard, []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	if len(f.attached) != 2 {
		t.Fatalf("want install + inner setup, got %q", f.attached)
	}
	if !strings.Contains(f.attached[0][len(f.attached[0])-1], "astrona-linux-") {
		t.Errorf("first attach is not the install script: %q", f.attached[0])
	}
	if got := strings.Join(f.attached[1], " "); !strings.HasSuffix(got, "astrona setup --yes") {
		t.Errorf("inner setup = %q", got)
	}
	if !pathed {
		t.Error("astrona.exe not put on PATH")
	}
}

func TestSetupRejectsUnknownFlags(t *testing.T) {
	if err := setup(&fakeWSL{}, strings.NewReader(""), io.Discard, []string{"--frobnicate"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
