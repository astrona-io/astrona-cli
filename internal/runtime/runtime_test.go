package runtime

import (
	"io"
	"testing"

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
