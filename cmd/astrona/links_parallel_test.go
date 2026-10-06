package main

import (
	"astrona/internal/lifecycle"
	"testing"
)

func TestValidateParallel(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, lifecycle.MaxParallel: true, lifecycle.MaxParallel + 1: false} {
		if (validateParallel(n) == nil) != ok {
			t.Errorf("validateParallel(%d) ok = %v", n, !ok)
		}
	}
}
