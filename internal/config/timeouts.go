package config

import (
	"fmt"
	"strings"
	"time"
)

// Time limits for scripts and checks. A script or check that runs longer
// is killed (with everything it started) and reported as timed out, so
// one hanging command can't hang `run`, `submit` or `destroy`.
const (
	// DefaultScriptTimeout bounds a bootstrap, testing or teardown script
	// (a whole folder source counts as one). Setup scripts install
	// packages and pull images, so this is generous.
	DefaultScriptTimeout = 30 * time.Minute
	// DefaultValidationScriptTimeout bounds a validation script: grading
	// reads state, it doesn't build it.
	DefaultValidationScriptTimeout = 5 * time.Minute
	// DefaultCheckTimeout bounds a validation check (one kubectl call, a
	// command or an HTTP request).
	DefaultCheckTimeout = time.Minute

	maxScriptTimeout = 2 * time.Hour
	maxCheckTimeout  = 30 * time.Minute
)

// EffectiveTimeout parses Timeout, or returns def when it's unset. Only
// call after ValidateTimeouts.
func (r ResourceItem) EffectiveTimeout(def time.Duration) time.Duration {
	return effectiveTimeout(r.Timeout, def)
}

// EffectiveTimeout parses Timeout, or returns DefaultCheckTimeout when
// it's unset. Only call after ValidateTimeouts.
func (c ValidationCheck) EffectiveTimeout() time.Duration {
	return effectiveTimeout(c.Timeout, DefaultCheckTimeout)
}

func effectiveTimeout(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func validateTimeout(where, s string, limit time.Duration) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%s: timeout '%s' is not a duration (e.g. '90s', '10m')", where, s)
	}
	if d < time.Second || d > limit {
		return fmt.Errorf("%s: timeout %s must be between 1s and %s", where, s, FormatDuration(limit))
	}
	return nil
}

// FormatDuration renders a time limit the way lab authors write it:
// "90s" → "1m30s", "5m", "2h" (time.Duration prints "5m0s", "2h0m0s").
func FormatDuration(d time.Duration) string {
	s := d.Round(time.Second).String()
	for _, tail := range []string{"m0s", "h0m"} {
		if strings.HasSuffix(s, tail) && len(s) > len(tail) {
			s = s[:len(s)-len(tail)+1]
		}
	}
	return s
}

// ValidateTimeouts checks every script's and check's timeout, and refuses
// one on a manifest (manifests are applied, not run, so it would do
// nothing).
func ValidateTimeouts(cfg *LabConfig) error {
	scripts := func(where string, items []ResourceItem) error {
		for i, it := range items {
			if err := validateTimeout(fmt.Sprintf("%s[%d] '%s'", where, i, it.Name), it.Timeout, maxScriptTimeout); err != nil {
				return err
			}
		}
		return nil
	}
	block := func(where string, b BootstrapConfig) error {
		for i, it := range b.Manifests {
			if it.Timeout != "" {
				return fmt.Errorf("%s.manifests[%d] '%s': timeout only applies to scripts, not manifests", where, i, it.Name)
			}
		}
		return scripts(where+".init", b.Init)
	}
	validation := func(where string, v ValidationConfig) error {
		for i, c := range v.Checks {
			if err := validateTimeout(fmt.Sprintf("%s.checks[%d] '%s'", where, i, c.Name), c.Timeout, maxCheckTimeout); err != nil {
				return err
			}
		}
		if v.Script != nil {
			if err := validateTimeout(fmt.Sprintf("%s.script '%s'", where, v.Script.Name), v.Script.Timeout, maxScriptTimeout); err != nil {
				return err
			}
		}
		return scripts(where+".scripts", v.Scripts)
	}

	if err := block("bootstrap", cfg.Bootstrap); err != nil {
		return err
	}
	if err := block("testing", cfg.Testing); err != nil {
		return err
	}
	if err := scripts("teardown.init", cfg.Teardown.Init); err != nil {
		return err
	}
	if err := validation("validation", cfg.Validation); err != nil {
		return err
	}
	for _, l := range cfg.KindClusters() {
		where := "runtime.kind.clusters[" + l.Name + "]"
		if err := block(where+".bootstrap", l.Bootstrap); err != nil {
			return err
		}
		if err := block(where+".testing", l.Testing); err != nil {
			return err
		}
		if err := scripts(where+".teardown.init", l.Teardown.Init); err != nil {
			return err
		}
	}
	for _, vm := range cfg.Runtime.QEMU {
		where := "runtime.qemu[" + vm.Name + "]"
		if vm.Bootstrap != nil {
			if err := block(where+".bootstrap", *vm.Bootstrap); err != nil {
				return err
			}
		}
		if vm.Validation != nil {
			if err := validation(where+".validation", *vm.Validation); err != nil {
				return err
			}
		}
	}
	return nil
}
