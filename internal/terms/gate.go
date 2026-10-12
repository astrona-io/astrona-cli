package terms

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// InCI recognizes common runners. CI never implies consent, even with a TTY.
func InCI(getenv func(string) string) bool {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "JENKINS_URL", "TF_BUILD", "CIRCLECI", "TEAMCITY_VERSION"} {
		value := strings.ToLower(strings.TrimSpace(getenv(key)))
		if value != "" && value != "false" && value != "0" {
			return true
		}
	}
	return false
}

// Ensure checks consent before command setup. CI requires the explicit flag on
// every invocation; elsewhere a current receipt also permits headless use.
func (s Store) Ensure(ctx context.Context, in io.Reader, out io.Writer, interactive, ci, explicit bool, cliVersion string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ci && !explicit {
		return fmt.Errorf("CI requires --accept-terms on each command; review `astrona terms` first")
	}
	accepted, err := s.Accepted()
	if err != nil {
		return err
	}
	if accepted && !explicit {
		return nil
	}
	method := "flag"
	if !explicit {
		if !interactive {
			return fmt.Errorf("current CLI terms have not been accepted; review `astrona terms`, then rerun with --accept-terms (or run from a terminal)")
		}
		if _, err := fmt.Fprintf(out, "%s\nSHA-256: %s\n\nAccept these CLI usage terms and acknowledge the risks? [y/N] ", Document(), Digest()); err != nil {
			return fmt.Errorf("display terms: %w", err)
		}
		if !readConsent(in) {
			return fmt.Errorf("terms not accepted; command cancelled")
		}
		method = "prompt"
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Accept(method, cliVersion); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Accepted Astrona CLI terms %s (SHA-256: %s).\n", Revision, Digest())
	return err
}

// Read only this answer, so a buffered reader cannot consume a later prompt's
// input. An incomplete line, blank answer or read error is not consent.
func readConsent(in io.Reader) bool {
	var answer strings.Builder
	var b [1]byte
	for answer.Len() < 32 {
		if _, err := io.ReadFull(in, b[:]); err != nil {
			return false
		}
		if b[0] == '\n' {
			value := strings.ToLower(strings.TrimSpace(answer.String()))
			return value == "y" || value == "yes"
		}
		answer.WriteByte(b[0])
	}
	return false
}
