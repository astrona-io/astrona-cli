package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func names(cs []cobra.Completion) []string {
	var out []string
	for _, c := range cs {
		out = append(out, strings.SplitN(c, "\t", 2)[0])
	}
	return out
}

func TestLabNameCandidates(t *testing.T) {
	rows := []labRow{
		{name: "astro-web", runtime: "kind", status: "Ready (1/1)"},
		{name: "astro-db", runtime: "kind", status: "Stopped"},
		{name: "astro-vm", runtime: "qemu", status: "Running"},
	}
	cases := []struct {
		keep       func(labRow) bool
		toComplete string
		want       string
	}{
		{nil, "", "astro-web,astro-db,astro-vm"},
		{nil, "astro-w", "astro-web"},
		{nil, "ast", "astro-web,astro-db,astro-vm"}, // still typing the prefix
		{nil, "w", "web"},                           // unprefixed form once it can't be the prefix
		{isKind, "", "astro-web,astro-db"},
		{isQEMU, "", "astro-vm"},
		{isStoppedKind, "", "astro-db"},
		{func(r labRow) bool { return isKind(r) && isRunning(r) }, "", "astro-web"},
		{nil, "zzz", ""},
	}
	for _, c := range cases {
		got := strings.Join(names(labNameCandidates(rows, c.keep, c.toComplete)), ",")
		if got != c.want {
			t.Errorf("toComplete=%q: got %q, want %q", c.toComplete, got, c.want)
		}
	}
	if desc := labNameCandidates(rows, nil, "astro-db")[0]; !strings.Contains(desc, "\tkind, Stopped") {
		t.Errorf("completion description = %q", desc)
	}
}
