package main

import (
	"astrona/internal/portforward"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// addOutputFlag adds -o/--output to cmd; formats lists the accepted values
// besides json (e.g. "wide"), for the help text.
func addOutputFlag(cmd *cobra.Command, output *string, formats ...string) {
	all := append([]string{"json"}, formats...)
	cmd.Flags().StringVarP(output, "output", "o", "", "Output format: "+strings.Join(all, " or "))
}

// checkOutput refuses an -o value the command doesn't support.
func checkOutput(output string, formats ...string) error {
	if output == "" || output == "json" {
		return nil
	}
	for _, f := range formats {
		if output == f {
			return nil
		}
	}
	return fmt.Errorf("unsupported --output '%s' (use %s)", output, strings.Join(append([]string{"json"}, formats...), " or "))
}

// printJSON writes v as indented JSON to stdout — the one format every
// `-o json` uses, for scripts, CI and UIs.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// checkResultJSON is a ✓/⚠/✗ line of check, doctor and validate.
type checkResultJSON struct {
	Section string `json:"section,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"` // ok | warn | fail
	Detail  string `json:"detail,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

func checkResultsJSON(section string, res []checkResult) []checkResultJSON {
	out := make([]checkResultJSON, 0, len(res))
	for _, r := range res {
		st := map[checkStatus]string{checkOK: "ok", checkWarn: "warn", checkFail: "fail"}[r.status]
		out = append(out, checkResultJSON{Section: section, Name: r.name, Status: st, Detail: r.detail, Fix: r.hint})
	}
	return out
}

// failedCount is how many of res failed.
func failedCount(res []checkResultJSON) int {
	n := 0
	for _, r := range res {
		if r.Status == "fail" {
			n++
		}
	}
	return n
}

// forwardJSON is a port forward in -o json output.
type forwardJSON struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	URL       string `json:"url"`
	Target    string `json:"target"`
	Namespace string `json:"namespace"`
	Cluster   string `json:"cluster,omitempty"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"lastError,omitempty"`
}

func forwardsJSON(fs []portforward.Forward) []forwardJSON {
	out := make([]forwardJSON, 0, len(fs))
	for _, f := range fs {
		pf := f.Spec.Forward.Normalized()
		out = append(out, forwardJSON{Name: pf.Name, State: string(f.Effective()), URL: portforward.LocalURL(pf),
			Target: fmt.Sprintf("%s:%d", pf.Resource, pf.TargetPort), Namespace: pf.Namespace, Cluster: pf.Cluster,
			Restarts: f.Status.Restarts, LastError: f.Status.LastError})
	}
	return out
}

// report prints ✓/⚠/✗ sections for people, or collects them into one JSON
// document (-o json) — so check, doctor and validate share one code path.
type report struct {
	json    bool
	results []checkResultJSON
	notes   []string
}

// section prints (or collects) res under title and returns how many failed.
func (r *report) section(title string, res []checkResult) int {
	r.results = append(r.results, checkResultsJSON(title, res)...)
	if r.json {
		n := 0
		for _, c := range res {
			if c.status == checkFail {
				n++
			}
		}
		return n
	}
	return printCheckResults(title, res)
}

// note prints a line for people; in JSON it's kept in "notes".
func (r *report) note(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if r.json {
		r.notes = append(r.notes, strings.TrimSpace(msg))
		return
	}
	fmt.Print(msg)
}

// done prints the JSON document (in JSON mode) and returns failErr when
// anything failed.
func (r *report) done(failErr error) error {
	if r.json {
		if err := printJSON(struct {
			OK       bool              `json:"ok"`
			Problems int               `json:"problems"`
			Results  []checkResultJSON `json:"results"`
			Notes    []string          `json:"notes,omitempty"`
		}{failedCount(r.results) == 0, failedCount(r.results), r.results, r.notes}); err != nil {
			return err
		}
	}
	if failedCount(r.results) > 0 {
		return failErr
	}
	return nil
}

// loadFailed ends validate and check when the lab config can't be loaded:
// under -o json it's still the one report, with err as a ✗ row under
// title, so stdout always carries a JSON document; otherwise just err.
func (r *report) loadFailed(title string, err error) error {
	if !r.json {
		return err
	}
	r.section(title, []checkResult{doctorLabError(err)})
	return r.done(err)
}
