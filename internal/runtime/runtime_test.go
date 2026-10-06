package runtime

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"astrona/internal/executor"
)

// fakeExecutor is a no-op ScriptExecutor for tests that only need to
// distinguish which one got picked, never actually run anything.
type fakeExecutor struct{ id string }

func (f fakeExecutor) RunScript(string, io.Writer) error { return nil }

func TestLabEnvironmentExecutorForVM(t *testing.T) {
	t.Run("vm name resolves to the matching executor", func(t *testing.T) {
		env := &LabEnvironment{Executors: map[string]executor.ScriptExecutor{
			"server": fakeExecutor{id: "server"},
			"client": fakeExecutor{id: "client"},
		}}

		got, err := env.ExecutorForVM("client")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.(fakeExecutor).id != "client" {
			t.Errorf("got %v, want client executor", got)
		}
	})

	t.Run("unknown vm name errors", func(t *testing.T) {
		env := &LabEnvironment{Executors: map[string]executor.ScriptExecutor{"server": fakeExecutor{}}}

		if _, err := env.ExecutorForVM("nonexistent"); err == nil {
			t.Error("expected error for unknown vm name")
		}
	})
}

// Concurrent kind creates (--parallel linked clusters) must not interleave
// their preserve→create→restore sections: if one preserves after another's
// create switched the context, the user ends up on a kind-… context.
func TestWithPreservedContextSerializesConcurrentCreates(t *testing.T) {
	var (
		mu      sync.Mutex
		current = "user-ctx" // the user's kubectl current-context
		events  []string
	)
	record := func(e string) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	const n = 8
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprint(i)
			err := withPreservedContext(
				func() func() {
					record("preserve " + id)
					mu.Lock()
					prev := current
					mu.Unlock()
					return func() {
						record("restore " + id)
						mu.Lock()
						current = prev
						mu.Unlock()
					}
				},
				func() error {
					record("create " + id)
					time.Sleep(5 * time.Millisecond)
					mu.Lock()
					current = "kind-astro-" + id // what kind create does
					mu.Unlock()
					time.Sleep(5 * time.Millisecond)
					return nil
				},
			)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if current != "user-ctx" {
		t.Errorf("current-context = %q after concurrent creates, want user-ctx", current)
	}
	if len(events) != 3*n {
		t.Fatalf("got %d events, want %d: %v", len(events), 3*n, events)
	}
	for i := 0; i < len(events); i += 3 {
		id := strings.TrimPrefix(events[i], "preserve ")
		want := []string{"preserve " + id, "create " + id, "restore " + id}
		if got := events[i : i+3]; !slices.Equal(got, want) {
			t.Fatalf("interleaved sections at %d: %v (all: %v)", i, got, events)
		}
	}
}

func TestWithPreservedContextRestoresOnError(t *testing.T) {
	restored := false
	want := errors.New("kind failed")
	err := withPreservedContext(
		func() func() { return func() { restored = true } },
		func() error { return want },
	)
	if !errors.Is(err, want) || !restored {
		t.Fatalf("err = %v, restored = %v; want kind error and restore called", err, restored)
	}
}
