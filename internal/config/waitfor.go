package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WaitFor is one entry in bootstrap.waitFor / testing.waitFor: a readiness
// gate run with kubectl after that stage's manifests are applied, so
// "ready" means the workloads are actually up — not just that `kubectl
// apply` returned.
//
// Exactly one of three targets:
//
//	resource: deploy/web                      one named object
//	resource: pod      + selector: app=web    every object matching a label selector
//	resource: node     + all: true            every object of that kind
type WaitFor struct {
	Name      string `yaml:"name"`
	Resource  string `yaml:"resource"`
	Selector  string `yaml:"selector"`
	All       bool   `yaml:"all"`
	Namespace string `yaml:"namespace"`
	// Condition is "rollout" (kubectl rollout status) or a status condition
	// for kubectl wait --for=condition=<Condition> ("Ready", "Available",
	// "Complete", "Established", …). Defaults per kind — see
	// defaultConditions.
	Condition string `yaml:"condition"`
	// Timeout is a Go duration ("90s", "5m"); default DefaultWaitTimeout.
	Timeout string `yaml:"timeout"`
}

const (
	DefaultWaitTimeout = 2 * time.Minute
	maxWaitTimeout     = 30 * time.Minute
	// ConditionRollout selects `kubectl rollout status` instead of
	// `kubectl wait`.
	ConditionRollout = "rollout"
)

// defaultConditions is the condition used when an entry sets none. Kinds
// not listed here must set condition explicitly.
var defaultConditions = map[string]string{
	"deploy": ConditionRollout, "deployment": ConditionRollout, "deployments": ConditionRollout,
	"sts": ConditionRollout, "statefulset": ConditionRollout, "statefulsets": ConditionRollout,
	"ds": ConditionRollout, "daemonset": ConditionRollout, "daemonsets": ConditionRollout,
	"po": "Ready", "pod": "Ready", "pods": "Ready",
	"no": "Ready", "node": "Ready", "nodes": "Ready",
	"job": "Complete", "jobs": "Complete",
	"crd": "Established", "customresourcedefinition": "Established", "customresourcedefinitions": "Established",
}

// rolloutKinds are the only kinds `kubectl rollout status` understands.
var rolloutKinds = map[string]bool{
	"deploy": true, "deployment": true, "deployments": true,
	"sts": true, "statefulset": true, "statefulsets": true,
	"ds": true, "daemonset": true, "daemonsets": true,
}

var (
	// waitKindPattern allows short names, plurals, and CRD-style
	// "<plural>.<group>" kinds (e.g. certificates.cert-manager.io).
	waitKindPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)*$`)
	waitObjectNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	waitNamespacePattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	waitConditionPattern  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,62}$`)
	// labelSelectorPattern is the character set of kubectl label selectors
	// (key=value, key!=value, key in (a,b), !key, …). Passed as a single
	// --selector=<value> argument, never through a shell.
	labelSelectorPattern = regexp.MustCompile(`^[A-Za-z0-9!][A-Za-z0-9._/=!,() -]{0,252}$`)
)

// Kind is the resource kind part ("deploy" in "deploy/web").
func (w WaitFor) Kind() string {
	kind, _, _ := strings.Cut(w.Resource, "/")
	return strings.ToLower(kind)
}

// ObjectName is the name part ("web" in "deploy/web"), "" for a
// selector/all entry.
func (w WaitFor) ObjectName() string {
	_, name, _ := strings.Cut(w.Resource, "/")
	return name
}

// EffectiveCondition applies the per-kind default.
func (w WaitFor) EffectiveCondition() string {
	if w.Condition != "" {
		return w.Condition
	}
	return defaultConditions[w.Kind()]
}

// EffectiveNamespace defaults to "default".
func (w WaitFor) EffectiveNamespace() string {
	if w.Namespace == "" {
		return "default"
	}
	return w.Namespace
}

// EffectiveTimeout parses Timeout, defaulting to DefaultWaitTimeout. Only
// call after Validate.
func (w WaitFor) EffectiveTimeout() time.Duration {
	if w.Timeout == "" {
		return DefaultWaitTimeout
	}
	d, _ := time.ParseDuration(w.Timeout)
	return d
}

// Label is how the entry is shown in progress output.
func (w WaitFor) Label() string {
	if w.Name != "" {
		return w.Name
	}
	switch {
	case w.Selector != "":
		return fmt.Sprintf("%s -l %s", w.Resource, w.Selector)
	case w.All:
		return w.Resource + " (all)"
	default:
		return w.Resource
	}
}

// Validate checks one entry. stage is "bootstrap" or "testing", for error
// messages.
func (w WaitFor) Validate(stage string) error {
	where := fmt.Sprintf("%s.waitFor '%s'", stage, w.Label())

	kind, name, hasName := strings.Cut(w.Resource, "/")
	if !waitKindPattern.MatchString(strings.ToLower(kind)) {
		return fmt.Errorf("%s: resource '%s' must be '<kind>/<name>' or a bare kind with selector/all (e.g. 'deploy/web', 'pod')", where, w.Resource)
	}

	targets := 0
	if hasName {
		if !waitObjectNamePattern.MatchString(name) {
			return fmt.Errorf("%s: '%s' is not a valid Kubernetes object name", where, name)
		}
		targets++
	}
	if w.Selector != "" {
		if !labelSelectorPattern.MatchString(w.Selector) {
			return fmt.Errorf("%s: selector '%s' is not a valid label selector", where, w.Selector)
		}
		targets++
	}
	if w.All {
		targets++
	}
	if targets != 1 {
		return fmt.Errorf("%s: set exactly one of a name ('%s/<name>'), selector, or all: true", where, kind)
	}

	if w.Namespace != "" && !waitNamespacePattern.MatchString(w.Namespace) {
		return fmt.Errorf("%s: namespace '%s' is not a valid Kubernetes namespace", where, w.Namespace)
	}

	switch cond := w.EffectiveCondition(); {
	case cond == "":
		return fmt.Errorf("%s: no default condition for kind '%s' — set condition (e.g. Ready, Available)", where, kind)
	case cond == ConditionRollout:
		if !rolloutKinds[strings.ToLower(kind)] {
			return fmt.Errorf("%s: condition 'rollout' only works for deployments, statefulsets and daemonsets", where)
		}
		if w.All {
			return fmt.Errorf("%s: condition 'rollout' needs a name or selector, not all: true", where)
		}
	case !waitConditionPattern.MatchString(cond):
		return fmt.Errorf("%s: condition '%s' must be 'rollout' or a status condition like 'Ready'", where, cond)
	}

	if w.Timeout != "" {
		d, err := time.ParseDuration(w.Timeout)
		if err != nil {
			return fmt.Errorf("%s: timeout '%s' is not a duration (e.g. '90s', '5m'): %w", where, w.Timeout, err)
		}
		if d <= 0 || d > maxWaitTimeout {
			return fmt.Errorf("%s: timeout %s must be between 1s and %s", where, d, maxWaitTimeout)
		}
	}

	return nil
}

// ValidateWaitFor checks every waitFor list in cfg. waitFor needs a
// kubectl-reachable cluster, so on qemu it's rejected outright (including
// inside a VM's own bootstrap block).
func ValidateWaitFor(cfg *LabConfig) error {
	isQEMU := cfg.Runtime.Type == "qemu"

	stages := []struct {
		name  string
		items []WaitFor
	}{
		{"bootstrap", cfg.Bootstrap.WaitFor},
		{"testing", cfg.Testing.WaitFor},
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Bootstrap != nil && len(vm.Bootstrap.WaitFor) > 0 {
			return fmt.Errorf("runtime.qemu vm '%s': bootstrap.waitFor needs a kubectl-reachable cluster, which the qemu runtime doesn't have", vm.Name)
		}
	}

	for _, s := range stages {
		if len(s.items) == 0 {
			continue
		}
		if isQEMU {
			return fmt.Errorf("%s.waitFor needs a kubectl-reachable cluster, which the qemu runtime doesn't have", s.name)
		}
		for _, w := range s.items {
			if err := w.Validate(s.name); err != nil {
				return err
			}
		}
	}
	return nil
}
