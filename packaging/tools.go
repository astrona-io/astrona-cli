// Package packaging exposes the tool versions and checksums pinned in
// tools.env. The Linux package build sources the same file, so the packages
// and `astrona setup` install the same kind and kubectl.
package packaging

import (
	_ "embed"
	"strings"
)

//go:embed tools.env
var toolsEnv string

// Pin is the value of key in tools.env ("KIND_VERSION", ...), or "" when
// tools.env does not set it.
func Pin(key string) string {
	for _, line := range strings.Split(toolsEnv, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
