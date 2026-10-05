package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// CheckTypes are the validation.checks types the Proctor runs (matched
// case-insensitively).
var CheckTypes = []string{"resourceExists", "podReady", "command", "jsonpath", "count", "http"}

// ValidateChecks checks each validation check has what its type needs.
// Only types it knows are judged — unknown types are reported by
// `astrona validate` and fail at grading.
func ValidateChecks(cfg *LabConfig) error {
	blocks := map[string]ValidationConfig{"validation": cfg.Validation}
	for _, vm := range cfg.Runtime.QEMU {
		if vm.Validation != nil {
			blocks["runtime.qemu["+vm.Name+"].validation"] = *vm.Validation
		}
	}
	for where, v := range blocks {
		for i, c := range v.Checks {
			if err := validateCheck(c); err != nil {
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
	switch strings.ToLower(c.Type) {
	case "jsonpath":
		if strings.TrimSpace(c.Resource) == "" {
			return fmt.Errorf("jsonpath check needs resource (e.g. \"deploy/web -n shop\")")
		}
		if !strings.HasPrefix(strings.TrimSpace(c.JSONPath), "{") {
			return fmt.Errorf("jsonpath must be a kubectl JSONPath expression like \"{.spec.replicas}\"")
		}
		if c.Expect == "" && c.Contains == "" && c.ExpectRegex == "" {
			return fmt.Errorf("jsonpath check needs expect, contains or expectRegex")
		}
	case "count":
		if strings.TrimSpace(c.Resource) == "" {
			return fmt.Errorf("count check needs resource (e.g. \"pods -l app=web -n shop\")")
		}
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
