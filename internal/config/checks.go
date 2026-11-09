package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// CheckTypes are the validation.checks types the Proctor runs (matched
// case-insensitively).
var CheckTypes = []string{"resourceExists", "podReady", "command", "jsonpath", "count", "http"}

// IsKubernetesCheck reports whether a check of type t runs kubectl against
// a lab's Kubernetes cluster (resourceExists, podReady, jsonpath, count).
func IsKubernetesCheck(t string) bool {
	switch strings.ToLower(t) {
	case "resourceexists", "podready", "jsonpath", "count":
		return true
	}
	return false
}

// ErrNothingToGrade is a lab with no validation checks and no validation
// scripts: grading it can't say anything, so it is an error, not a pass.
var ErrNothingToGrade = errors.New("this lab has nothing to grade — it defines no validation.checks and no validation scripts")

// HasGrading reports whether cfg defines any validation check or script,
// at the root or on a runtime.qemu VM.
func (cfg *LabConfig) HasGrading() bool {
	has := func(v ValidationConfig) bool {
		return len(v.Checks) > 0 || (v.Script != nil && v.Script.Source != "") || len(v.Scripts) > 0
	}
	if has(cfg.Validation) {
		return true
	}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil && has(*vm.Validation) {
			return true
		}
	}
	return false
}

// resourceFlags are the only kubectl flags a check's resource may carry,
// each taking a value (except the -A forms) — everything else starting
// with "-" (--kubeconfig, --server, --context, -o, …) would change which
// cluster kubectl talks to or what it prints.
var resourceFlags = map[string]bool{
	"-n": true, "--namespace": true,
	"-l": true, "--selector": true,
	"--field-selector": true,
	"-A":               false, "--all-namespaces": false,
}

// ResourceArgs splits a check's resource into kubectl arguments, refusing
// any flag but a namespace, label/field selector or --all-namespaces, and
// any value that starts with "-".
func ResourceArgs(resource string) ([]string, error) {
	args := strings.Fields(resource)
	if len(args) == 0 {
		return nil, fmt.Errorf("resource is empty")
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, value, inline := strings.Cut(a, "=")
		takesValue, ok := resourceFlags[name]
		if !ok {
			return nil, fmt.Errorf("resource '%s': flag '%s' isn't allowed — only -n/--namespace, -l/--selector, --field-selector and -A/--all-namespaces", resource, name)
		}
		switch {
		case !takesValue && inline:
			return nil, fmt.Errorf("resource '%s': %s takes no value", resource, name)
		case takesValue && inline:
			if value == "" || strings.HasPrefix(value, "-") {
				return nil, fmt.Errorf("resource '%s': %s needs a value that doesn't start with '-'", resource, name)
			}
		case takesValue:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, fmt.Errorf("resource '%s': %s needs a value that doesn't start with '-'", resource, name)
			}
			i++
		}
	}
	return args, nil
}

// ValidateChecks checks each validation check has what its type needs.
// Only types it knows are judged — unknown types are reported by
// `astrona validate` and fail at grading. Kubernetes checks are refused on
// the qemu runtime: it has no lab cluster for kubectl, and they must never
// fall back to the user's own kubeconfig.
func ValidateChecks(cfg *LabConfig) error {
	blocks := map[string]ValidationConfig{"validation": cfg.Validation}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			blocks["runtime.qemu["+vm.Name+"].validation"] = *vm.Validation
		}
	}
	isQEMU := cfg.Runtime.Type == "qemu"
	for where, v := range blocks {
		for i, c := range v.Checks {
			err := validateCheck(c)
			if err == nil && isQEMU && IsKubernetesCheck(c.Type) {
				err = fmt.Errorf("%s checks need a Kubernetes cluster from the lab, which the qemu runtime doesn't have — use a validation script, which runs inside the VM", c.Type)
			}
			if err != nil {
				return fmt.Errorf("%s.checks[%d] '%s': %w", where, i, c.Name, err)
			}
		}
	}
	return nil
}

func validateCheck(c ValidationCheck) error {
	if c.ExpectRegex != "" {
		if _, err := regexp.Compile(c.ExpectRegex); err != nil {
			return fmt.Errorf("expectRegex is not a valid regular expression: %w", err)
		}
	}
	if IsKubernetesCheck(c.Type) {
		if strings.TrimSpace(c.Resource) == "" {
			return fmt.Errorf("%s check needs resource (e.g. \"deploy/web -n shop\")", c.Type)
		}
		if _, err := ResourceArgs(c.Resource); err != nil {
			return err
		}
	}
	switch strings.ToLower(c.Type) {
	case "jsonpath":
		if !strings.HasPrefix(strings.TrimSpace(c.JSONPath), "{") {
			return fmt.Errorf("jsonpath must be a kubectl JSONPath expression like \"{.spec.replicas}\"")
		}
		if c.Expect == "" && c.Contains == "" && c.ExpectRegex == "" {
			return fmt.Errorf("jsonpath check needs expect, contains or expectRegex")
		}
	case "count":
		if c.Min == nil && c.Max == nil {
			return fmt.Errorf("count check needs min and/or max")
		}
		if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			return fmt.Errorf("min %d is greater than max %d", *c.Min, *c.Max)
		}
		if (c.Min != nil && *c.Min < 0) || (c.Max != nil && *c.Max < 0) {
			return fmt.Errorf("min/max can't be negative")
		}
	case "http":
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("http check needs an http(s) url, got '%s'", c.URL)
		}
		if c.ExpectStatus != 0 && (c.ExpectStatus < 100 || c.ExpectStatus > 599) {
			return fmt.Errorf("expectStatus %d is not an HTTP status", c.ExpectStatus)
		}
	}
	return nil
}
