package ui

import (
	"bytes"
	"testing"
)

func TestPaintOnlyOnTerminals(t *testing.T) {
	var buf bytes.Buffer
	if got := Paint(&buf, "x", Red, Bold); got != "x" {
		t.Errorf("non-terminal got escape codes: %q", got)
	}
	if got := PassFail(&buf, false, 4); got != "FAIL" {
		t.Errorf("PassFail = %q", got)
	}
	if got := PassFail(&buf, true, 6); got != "PASS  " {
		t.Errorf("PassFail padding = %q", got)
	}
}
