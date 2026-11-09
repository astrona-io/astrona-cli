//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// A signal must cancel the context without killing the process — so
// `astrona test` can unwind into its deferred diagnostics and teardown —
// and a second one must not kill it either (CI sends SIGINT then SIGTERM).
// SIGUSR1 stands in for SIGINT/SIGTERM: its default action also terminates
// the process, so the test binary dying would fail this test.
func TestCancelOnSignal(t *testing.T) {
	type call struct {
		sig   os.Signal
		first bool
	}
	calls := make(chan call, 4)
	ctx, stop := cancelOnSignal(func(sig os.Signal, first bool) { calls <- call{sig, first} }, syscall.SIGUSR1)
	defer stop()

	if ctx.Err() != nil {
		t.Fatal("context cancelled before any signal")
	}
	for i, wantFirst := range []bool{true, false} {
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		select {
		case c := <-calls:
			if c.sig != syscall.SIGUSR1 || c.first != wantFirst {
				t.Fatalf("signal %d: got %v first=%t, want SIGUSR1 first=%t", i+1, c.sig, c.first, wantFirst)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("signal %d never reached the handler", i+1)
		}
		if ctx.Err() == nil {
			t.Fatalf("signal %d did not cancel the context", i+1)
		}
	}
}

// stop cancels the context too and is safe to call twice (defer + early call).
func TestCancelOnSignalStop(t *testing.T) {
	ctx, stop := cancelOnSignal(nil, syscall.SIGUSR1)
	stop()
	stop()
	if ctx.Err() == nil {
		t.Fatal("stop did not cancel the context")
	}
}
