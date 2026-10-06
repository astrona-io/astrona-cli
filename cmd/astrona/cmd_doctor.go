package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/diagnostics"
	"astrona/internal/exam"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/runtime"
	"astrona/internal/ui"
	"astrona/internal/version"

	"github.com/spf13/cobra"
)

// maxDoctorPods bounds the unhealthy pods doctor lists per cluster.
const maxDoctorPods = 5

func newDoctorCmd(flags *rootFlags) *cobra.Command {
	var bundle bool
	var output string
	cmd := &cobra.Command{
		Use:   "doctor [lab]",
		Short: "Find out why something isn't working: this machine, the lab config and the running lab",
		Long: "Check everything a lab depends on and say what's wrong and how to fix it, in one go:\n\n" +
			"  This machine   required tools, the container engine (running? memory, CPUs), inotify\n" +
			"  Lab            its config, whether this astrona may run it (astronaVersion), memory and\n" +
			"                 free host ports (when not running)\n" +
			"  Running lab    nodes, linked clusters, port forwards, unhealthy pods, exam clock\n\n" +
			"Only reads — nothing is started, installed or changed. Exits non-zero on any ✗. --bundle " +
			"also writes the full diagnostics bundle (`astrona diagnose`) of a running lab.\n\n" +
			"The lab is the one other commands would use: the argument, -c/--git, the current " +
			"directory, or `astrona use`. `astrona check` and `astrona diagnose` remain for the " +
			"machine alone and for the bundle alone.",
		Example: `  astrona doctor
  astrona doctor ./labs/net-01
  astrona doctor --bundle`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := labArg(args, flags); err != nil { // a lab given as the argument wins over `astrona use`
				return err
			}
			if err := checkOutput(output); err != nil {
				return err
			}
			rep := &report{json: output == "json"}
			failed := 0

			machine, engine := doctorMachine()
			failed += rep.section("This machine", machine)

			cfg, baseDir, err := doctorLoadLab(flags)
			if err != nil {
				failed += rep.section("Lab", []checkResult{doctorLabError(err)})
				return doctorVerdict(rep, failed)
			}
			if cfg == nil {
				rep.note("\nNo lab here — pass one (astrona doctor ./path/to/lab), -c, or pick one with `astrona use`.\n")
				return doctorVerdict(rep, failed)
			}
			clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
			running := kindClusterExists(clusterName) || qemuStateExists(clusterName)

			labRes := []checkResult{doctorVersion(cfg)}
			if running {
				labRes = append(labRes, labConfigResults(cfg, baseDir)...)
			} else {
				labRes = append(labRes, checkLab(cfg, baseDir, engine)...)
			}
			failed += rep.section("Lab "+cfg.Metadata.Name, labRes)

			if !running {
				rep.note("\nThe lab isn't running — start it: astrona run\n")
				return doctorVerdict(rep, failed)
			}
			failed += rep.section("Running lab "+clusterName, doctorRunning(clusterName))

			if bundle {
				if env, err := runtime.LoadEnvironment(clusterName, cfg.Runtime); err == nil {
					rep, _ := ui.NewReporter("doctor", cfg.Metadata.Name, flags.verbose)
					collectDiagnostics(env, cfg, clusterName, "", rep)
					rep.Close()
				}
			}
			return doctorVerdict(rep, failed)
		},
	}
	cmd.Flags().BoolVar(&bundle, "bundle", false, "Also write the full diagnostics bundle of the running lab")
	addOutputFlag(cmd, &output)
	return cmd
}

func doctorVerdict(rep *report, failed int) error {
	err := fmt.Errorf("%d problem(s) found — the ✗ lines above say how to fix each", failed)
	if rep.json {
		return rep.done(err)
	}
	fmt.Println()
	if failed > 0 {
		return err
	}
	fmt.Println("No problems found. (⚠ lines are worth a look but don't block anything.)")
	return nil
}

// doctorMachine: required tools, the container engine, inotify.
func doctorMachine() ([]checkResult, *engineInfo) {
	var res []checkResult
	for _, c := range astronaDepChecks() {
		if !c.required {
			continue
		}
		if found, detail := c.find(); found {
			res = append(res, checkResult{name: c.name, detail: detail})
		} else {
			res = append(res, checkResult{status: checkFail, name: c.name, detail: "not found — " + c.note, hint: "install: " + c.installHint})
		}
	}
	if _, err := cluster.DetectContainerEngine(); err != nil {
		res = append(res, checkResult{status: checkFail, name: "container engine", detail: err.Error()})
		return res, nil
	}
	engineRes, engine := checkEngine()
	res = append(res, engineRes...)
	if goruntime.GOOS == "linux" {
		res = append(res, checkInotify("/proc")...)
	}
	return res, engine
}

// doctorLoadLab reads the lab config the command would use — without the
// hand-over to another astrona version that loading normally does:
// doctor reports, it doesn't act. A nil config with a nil error means no
// lab was named and the current directory has none; any other failure —
// a named lab that isn't there, a config that doesn't parse — is an error
// for doctor to report.
func doctorLoadLab(flags *rootFlags) (*config.LabConfig, string, error) {
	path, err := config.ResolveConfigPath(flags.configPath, flags.fileName, flags.gitURL, flags.gitRef, false)
	if err != nil {
		return nil, "", noLabOr(err, flags)
	}
	cfg, cleanup, err := config.LoadLabConfig(path)
	if err != nil {
		return nil, "", noLabOr(err, flags)
	}
	cleanup()
	baseDir := ""
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		baseDir = filepath.Dir(path)
	}
	return cfg, baseDir, nil
}

// noLabOr is nil for "no lab named, none in the current directory" (the
// same case withNoLabHint covers) and err otherwise.
func noLabOr(err error, flags *rootFlags) error {
	if errors.Is(err, os.ErrNotExist) && flags.configPath == "." && flags.gitURL == "" {
		return nil
	}
	return err
}

// doctorLabError is the ✗ row for a lab that was named but can't be loaded.
func doctorLabError(err error) checkResult {
	r := checkResult{status: checkFail, name: "lab config", detail: err.Error()}
	switch {
	case labVersionFromLoadError(err) != "":
		r.hint = "this lab needs astrona " + labVersionFromLoadError(err) + " — `astrona run` offers to install it"
	case errors.Is(err, os.ErrNotExist):
		r.hint = "check the path (argument, -c, --file) or pick another lab with `astrona use`"
	default:
		r.hint = "fix the config, then `astrona validate` for the details"
	}
	return r
}

// doctorVersion: may this astrona run the lab (astronaVersion)?
func doctorVersion(cfg *config.LabConfig) checkResult {
	r := checkResult{name: "astrona version"}
	if cfg.AstronaVersion == "" {
		r.detail = "any astrona may run it"
		return r
	}
	c, err := version.ParseConstraint(cfg.AstronaVersion)
	if err != nil {
		r.status, r.detail = checkFail, "astronaVersion: "+err.Error()
		return r
	}
	cur, err := version.Parse(Version)
	switch {
	case err != nil:
		r.detail = fmt.Sprintf("needs %s; this is a %s build (not checked)", c, Version)
	case c.Allows(cur):
		r.detail = fmt.Sprintf("needs %s — this astrona (%s) fits", c, cur)
	default:
		if iv, ok := newestAllowed(c, installedVersions()); ok {
			r.status = checkWarn
			r.detail = fmt.Sprintf("needs %s — commands run it with astrona %s (installed)", c, iv.v)
		} else {
			r.status = checkFail
			r.detail = fmt.Sprintf("needs %s, this is %s, and no fitting version is installed", c, cur)
			r.hint = "astrona versions available, then astrona versions install <version>"
		}
	}
	return r
}

// doctorRunning: nodes, linked clusters, port forwards, unhealthy pods,
// exam clock.
func doctorRunning(clusterName string) []checkResult {
	var res []checkResult
	if qemuStateExists(clusterName) {
		return append(res, checkResult{name: "qemu VM(s)", detail: "running — `astrona ssh` to look inside"})
	}

	nodes := checkResult{name: "nodes"}
	health, _ := kindAPIHealth(clusterName)
	nodes.detail = health
	switch {
	case strings.HasPrefix(health, "Ready"):
	case strings.HasPrefix(health, "Stopped"):
		nodes.status, nodes.hint = checkFail, "astrona start"
	default:
		nodes.status, nodes.hint = checkFail, "give it a minute after a start; if it stays like this: astrona reset (or reset --soft)"
	}
	res = append(res, nodes)

	clusters := []diagnostics.Kind{{Name: clusterName, KubeContext: "kind-" + clusterName, Kubeconfig: cluster.ExistingKubeconfig(clusterName)}}
	links, _ := lifecycle.Links(clusterName)
	for _, l := range links {
		r := checkResult{name: "linked cluster " + l.Name}
		row, ok := findLabRow(l.Cluster)
		switch {
		case !ok:
			r.status, r.detail, r.hint = checkFail, "not running", "astrona reset --cluster "+l.Name
		case strings.HasPrefix(row.status, "Ready"):
			r.detail = row.status + " · " + l.Hostname()
			clusters = append(clusters, diagnostics.Kind{Name: l.Cluster, KubeContext: l.Context(), Kubeconfig: cluster.ExistingKubeconfig(l.Cluster)})
		default:
			r.status, r.detail, r.hint = checkFail, row.status, "astrona start (starts the lab's linked clusters too)"
		}
		res = append(res, r)
	}

	fs, _ := portforward.List(clusterName)
	for _, f := range fs {
		r := checkResult{name: "port forward " + f.Spec.Forward.Name, detail: string(f.Effective()) + " · " + portforward.LocalURL(f.Spec.Forward)}
		switch f.Effective() {
		case portforward.StateReady:
		case portforward.StateNotReady:
			r.status, r.hint = checkWarn, "still retrying — `astrona port-forward list` shows why"
		default:
			r.status = checkFail
			if f.Status.LastError != "" {
				r.detail += " · " + f.Status.LastError
			}
			r.hint = "astrona port-forward start"
		}
		res = append(res, r)
	}

	for _, k := range clusters {
		pods, err := diagnostics.UnhealthyPods(k)
		r := checkResult{name: "pods in " + k.Name}
		switch {
		case err != nil:
			r.status, r.detail = checkWarn, err.Error()
		case len(pods) == 0:
			r.detail = "all healthy"
		default:
			// Unhealthy pods may well be the exercise — worth a look, not
			// a failure of the lab.
			r.status = checkWarn
			shown := pods
			if len(shown) > maxDoctorPods {
				shown = shown[:maxDoctorPods]
			}
			r.detail = fmt.Sprintf("%d unhealthy: %s", len(pods), strings.Join(shown, "; "))
			r.hint = "astrona diagnose collects their logs and events"
		}
		res = append(res, r)
	}

	if st, err := exam.Load(clusterName); err == nil && st != nil {
		r := checkResult{name: "exam clock", detail: st.Summary(time.Now())}
		if st.Over(time.Now()) {
			r.status, r.hint = checkWarn, "astrona submit to record your result"
		}
		res = append(res, r)
	}
	return res
}
