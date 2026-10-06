package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestVerifyProvenance(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "astrona-x")
	os.WriteFile(bin, []byte("binary"), 0755)

	t.Setenv("PATH", t.TempDir()) // no gh at all
	if p, note, err := verifyProvenance(bin, "v0.2.1"); err != nil || p != provenanceUnavailable || !strings.Contains(note, "install the GitHub CLI") {
		t.Errorf("no gh = %v %q %v", p, note, err)
	}

	fakeGH(t, `echo "Loaded digest sha256:abc"; exit 0`)
	if p, _, err := verifyProvenance(bin, "v0.2.1"); err != nil || p != provenanceVerified {
		t.Errorf("verified = %v %v", p, err)
	}

	fakeGH(t, `echo "Error: no attestations found for subject" >&2; exit 1`)
	if p, note, err := verifyProvenance(bin, "v0.2.1"); err != nil || p != provenanceUnavailable || !strings.Contains(note, "predates") {
		t.Errorf("older release = %v %q %v", p, note, err)
	}

	fakeGH(t, `echo "Error: HTTP 404: Not Found (https://api.github.com/repos/x/attestations/sha256:abc)" >&2; exit 1`)
	if _, note, err := verifyProvenance(bin, "v0.2.1"); err != nil || !strings.Contains(note, "predates") {
		t.Errorf("API 404 (older release) = %q %v", note, err)
	}

	fakeGH(t, `echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4`)
	if _, note, err := verifyProvenance(bin, "v0.2.1"); err != nil || !strings.Contains(note, "gh auth login") {
		t.Errorf("not logged in = %q %v", note, err)
	}

	// An attestation that exists but doesn't match: refuse.
	fakeGH(t, `echo "Error: verifying with issuer: certificate identity mismatch" >&2; exit 1`)
	if _, _, err := verifyProvenance(bin, "v0.2.1"); err == nil || !strings.Contains(err.Error(), "did NOT verify") {
		t.Errorf("mismatch must refuse: %v", err)
	}

	// A release newer than the last unattested one must have one.
	fakeGH(t, `echo "Error: HTTP 404: Not Found" >&2; exit 1`)
	if _, _, err := verifyProvenance(bin, "v0.2.3"); err == nil || !strings.Contains(err.Error(), "should carry") {
		t.Errorf("new release without attestation must refuse: %v", err)
	}
}
