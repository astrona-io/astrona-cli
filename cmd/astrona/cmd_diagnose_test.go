package main

import (
	"errors"
	"testing"
)

func TestWantDiagnostics(t *testing.T) {
	fail := errors.New("boom")
	cases := []struct {
		mode string
		err  error
		want bool
	}{
		{"on-failure", fail, true},
		{"on-failure", nil, false},
		{"always", nil, true},
		{"never", fail, false},
	}
	for _, c := range cases {
		if got := wantDiagnostics(c.mode, c.err); got != c.want {
			t.Errorf("wantDiagnostics(%s, %v) = %v", c.mode, c.err, got)
		}
	}
	if validateDiagnosticsMode("sometimes") == nil {
		t.Error("invalid mode accepted")
	}
}
