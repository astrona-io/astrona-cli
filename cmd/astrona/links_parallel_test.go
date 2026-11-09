package main

import (
	"astrona/internal/lifecycle"
	"astrona/internal/settings"
	"testing"
)

func TestValidateParallel(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, lifecycle.MaxParallel: true, lifecycle.MaxParallel + 1: false} {
		if (settings.ValidateParallel(n) == nil) != ok {
			t.Errorf("ValidateParallel(%d) ok = %v", n, !ok)
		}
	}
}
