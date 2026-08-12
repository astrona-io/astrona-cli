package main

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
		if got := isMultiVM(c.vms); got != c.want {
			t.Errorf("isMultiVM(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateQEMUVMs(t *testing.T) {
	t.Run("empty errors", func(t *testing.T) {
		if err := validateQEMUVMs(nil); err == nil {
			t.Error("expected error for empty runtime.qemu")
		}
	})

	t.Run("single unnamed entry is fine", func(t *testing.T) {
		if err := validateQEMUVMs([]QEMUVM{{Name: ""}}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("well-formed multi-VM is fine", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "client"}}
		if err := validateQEMUVMs(vms); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing name errors once multi-VM", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "  "}}
		if err := validateQEMUVMs(vms); err == nil {
			t.Error("expected error for unnamed vm")
		}
	})

	t.Run("duplicate name errors", func(t *testing.T) {
		vms := []QEMUVM{{Name: "server"}, {Name: "server"}}
		if err := validateQEMUVMs(vms); err == nil {
			t.Error("expected error for duplicate vm name")
		}
	})
}
