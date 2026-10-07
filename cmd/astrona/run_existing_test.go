package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestChooseRun(t *testing.T) {
	others := []string{"astro-net-01", "astro-db-02"}
	for _, c := range []struct {
		name               string
		tty, yes, exists   bool
		answers            string // one line per prompt
		others             []string
		want               runChoice
		wantErr, wantAsked string
	}{
		{name: "nothing running", tty: true, want: runChoice{}},
		{name: "running, yes to start over", tty: true, exists: true, answers: "y\n",
			want: runChoice{startOver: true}, wantAsked: "is already running. Destroy it and start over?"},
		{name: "running, no keeps it", tty: true, exists: true, answers: "\n",
			want: runChoice{keep: true}},
		{name: "running, --yes doesn't ask", exists: true, yes: true,
			want: runChoice{startOver: true}},
		{name: "running, no terminal, no --yes", exists: true,
			wantErr: "pass --yes to destroy it and start over"},
		{name: "others, yes stops them", tty: true, answers: "y\n", others: others,
			want: runChoice{stopOthers: others}, wantAsked: "Also running: net-01, db-02. Destroy them first?"},
		{name: "others, no keeps them", tty: true, answers: "n\n", others: others,
			want: runChoice{}},
		{name: "both: restart, and stop the other", tty: true, exists: true, answers: "y\ny\n", others: others[:1],
			want: runChoice{startOver: true, stopOthers: others[:1]}, wantAsked: "Destroy it first?"},
		{name: "--yes never stops others", tty: true, exists: true, yes: true, others: others,
			want: runChoice{startOver: true}},
		{name: "no terminal never asks about others", others: others,
			want: runChoice{}},
		{name: "kept lab: others not asked", tty: true, exists: true, answers: "n\n", others: others,
			want: runChoice{keep: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := chooseRun(strings.NewReader(c.answers), &out, c.tty, c.yes, c.exists, "lab-01", c.others)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("choice = %+v, want %+v", got, c.want)
			}
			if c.wantAsked != "" && !strings.Contains(out.String(), c.wantAsked) {
				t.Errorf("prompt %q missing in:\n%s", c.wantAsked, out.String())
			}
			if !c.tty && out.Len() > 0 {
				t.Errorf("asked without a terminal:\n%s", out.String())
			}
		})
	}
}
