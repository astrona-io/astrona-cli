package config

import (
	"strings"
	"testing"
)

func TestWANConditions(t *testing.T) {
	w := WANConditions{Latency: "150ms", Jitter: "20ms", Loss: "2%", Rate: "10mbit"}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.NetemArgs(), " "); got != "netem delay 150ms 20ms loss 2% rate 10mbit" {
		t.Errorf("NetemArgs = %s", got)
	}
	if got := strings.Join((WANConditions{Loss: "100%"}).NetemArgs(), " "); got != "netem loss 100%" {
		t.Errorf("loss only = %s", got)
	}
	for name, bad := range map[string]WANConditions{
		"latency word":   {Latency: "slow"},
		"shell":          {Latency: "1ms; reboot"},
		"jitter alone":   {Jitter: "5ms"},
		"loss no %":      {Loss: "5"},
		"loss over 100":  {Loss: "150%"},
		"rate unit":      {Rate: "10MB"},
		"negative":       {Latency: "-5ms"},
		"option smuggle": {Rate: "10mbit limit"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if !(WANConditions{}).IsZero() || w.IsZero() {
		t.Error("IsZero")
	}
}
