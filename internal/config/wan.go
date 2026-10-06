package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// WANConditions simulate a remote site for a linked cluster
// (runtime.kind.labs[].wan): `tc netem` on every node of the cluster adds
// latency (once per round trip — it delays what leaves the cluster),
// jitter, packet loss and a bandwidth cap to all its traffic. Applied once
// the cluster is set up, so its own bootstrap isn't slowed.
type WANConditions struct {
	// Latency added to everything the cluster sends, e.g. "80ms".
	Latency string `yaml:"latency"`
	// Jitter varies the latency by up to this much, e.g. "10ms".
	Jitter string `yaml:"jitter"`
	// Loss drops this share of packets, e.g. "1%" (0-100%).
	Loss string `yaml:"loss"`
	// Rate caps bandwidth, e.g. "10mbit" (kbit, mbit or gbit).
	Rate string `yaml:"rate"`
}

var (
	wanDuration = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,3})?(us|ms|s)$`)
	wanPercent  = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3})?%$`)
	wanRate     = regexp.MustCompile(`^[0-9]{1,6}(kbit|mbit|gbit)$`)
)

// IsZero reports whether no condition is set.
func (w WANConditions) IsZero() bool { return w == WANConditions{} }

// Validate checks every value against a strict pattern — they end up as
// `tc` arguments.
func (w WANConditions) Validate() error {
	if w.Latency != "" && !wanDuration.MatchString(w.Latency) {
		return fmt.Errorf("wan.latency '%s' must be a duration like 80ms", w.Latency)
	}
	if w.Jitter != "" {
		if !wanDuration.MatchString(w.Jitter) {
			return fmt.Errorf("wan.jitter '%s' must be a duration like 10ms", w.Jitter)
		}
		if w.Latency == "" {
			return fmt.Errorf("wan.jitter needs wan.latency")
		}
	}
	if w.Loss != "" {
		if !wanPercent.MatchString(w.Loss) {
			return fmt.Errorf("wan.loss '%s' must be a percentage like 1%%", w.Loss)
		}
		if v, _ := strconv.ParseFloat(strings.TrimSuffix(w.Loss, "%"), 64); v > 100 {
			return fmt.Errorf("wan.loss '%s' is more than 100%%", w.Loss)
		}
	}
	if w.Rate != "" && !wanRate.MatchString(w.Rate) {
		return fmt.Errorf("wan.rate '%s' must look like 10mbit (kbit, mbit or gbit)", w.Rate)
	}
	return nil
}

// NetemArgs is the netem part of a `tc qdisc` command for w.
func (w WANConditions) NetemArgs() []string {
	args := []string{"netem"}
	if w.Latency != "" {
		args = append(args, "delay", w.Latency)
		if w.Jitter != "" {
			args = append(args, w.Jitter)
		}
	}
	if w.Loss != "" {
		args = append(args, "loss", w.Loss)
	}
	if w.Rate != "" {
		args = append(args, "rate", w.Rate)
	}
	return args
}
