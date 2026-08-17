package config

import "testing"

func TestIsMultiVM(t *testing.T) {
	cases := []struct {
		name string
		vms  []QEMUVM
		want bool
	}{
		{"empty", nil, true},
		{"single unnamed", []QEMUVM{{Name: ""}}, false},
		{"single named", []QEMUVM{{Name: "server"}}, true},
		{"two entries", []QEMUVM{{Name: "server"}, {Name: "client"}}, true},
	}

	for _, c := range cases {
		if got := IsMultiVM(c.vms); got != c.want {
			t.Errorf("IsMultiVM(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateQEMUVMs(t *testing.T) {
	t.Run("empty errors", func(t *testing.T) {
		if err := ValidateQEMUVMs(nil); err == nil {
			t.Error("expected error for empty runtime.qemu")
		}
	})

	t.Run("single unnamed entry is fine", func(t *testing.T) {
		if err := ValidateQEMUVMs([]QEMUVM{{Name: ""}}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("well-formed multi-VM is fine", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "client"}}
		if err := ValidateQEMUVMs(vms); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing name errors once multi-VM", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "  "}}
		if err := ValidateQEMUVMs(vms); err == nil {
			t.Error("expected error for unnamed vm")
		}
	})

	t.Run("duplicate name errors", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "server"}}
		if err := ValidateQEMUVMs(vms); err == nil {
			t.Error("expected error for duplicate vm name")
		}
	})
}

func TestNormalizeClusterName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "astro-astrona-lab"},
		{"my-lab", "astro-my-lab"},
		{"astro-my-lab", "astro-my-lab"},
	}

	for _, c := range cases {
		if got := NormalizeClusterName(c.input); got != c.want {
			t.Errorf("NormalizeClusterName(%q) = %q, want %v", c.input, got, c.want)
		}
	}
}

func TestNormalizeTestClusterName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "astro-test-astrona-lab"},
		{"my-lab", "astro-test-my-lab"},
		{"astro-my-lab", "astro-test-my-lab"},
		{"test-my-lab", "astro-test-my-lab"},
		{"astro-test-my-lab", "astro-test-my-lab"},
	}

	for _, c := range cases {
		if got := NormalizeTestClusterName(c.input); got != c.want {
			t.Errorf("NormalizeTestClusterName(%q) = %q, want %v", c.input, got, c.want)
		}
	}
}
