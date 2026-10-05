// Package portforward runs and tracks the host-side `kubectl port-forward`s
// a kind lab declares in runtime.portForwards.
//
// Each forward gets one detached supervisor process (`astrona port-forward
// supervise <lab> <name>`, see Supervise) that keeps kubectl running and
// restarts it with backoff whenever it exits — plain `kubectl port-forward`
// dies as soon as the pod behind it restarts. Everything about a forward
// lives in its own directory, ~/.astrona/portforward/<lab>/<name>/:
//
//	spec.json       what to forward (written once, before the supervisor starts)
//	supervisor.pid  the supervisor's PID (its own process group leader)
//	status.json     the supervisor's latest view of kubectl (see Status)
//	supervisor.log  the supervisor's own log, kubectl's stderr included
package portforward

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrona/internal/config"
)

// State is a forward's readiness as reported by `astrona port-forward list`.
type State string

const (
	// StateReady: kubectl printed "Forwarding from ..." and the local port
	// accepts connections.
	StateReady State = "Ready"
	// StateNotReady: supervisor alive, kubectl not (yet/again) forwarding —
	// pod not running, no endpoints, or waiting out a restart backoff.
	StateNotReady State = "NotReady"
	// StateError: the same kubectl failure repeated errorThreshold times in
	// a row. Still retrying, but it most likely needs a human.
	StateError State = "Error"
	// StateStopped: the supervisor is gone.
	StateStopped State = "Stopped"
)

// Spec is everything the supervisor needs to run one forward.
type Spec struct {
	Lab         string             `json:"lab"`
	KubeContext string             `json:"kubeContext"`
	Forward     config.PortForward `json:"forward"`
	StartedAt   time.Time          `json:"startedAt"`
}

// Status is what the supervisor writes every time kubectl changes state.
type Status struct {
	State      State     `json:"state"`
	Since      time.Time `json:"since"`
	Restarts   int       `json:"restarts"`
	LastError  string    `json:"lastError,omitempty"`
	KubectlPID int       `json:"kubectlPid,omitempty"`
}

// Forward is one forward as found on disk, with its effective (live
// checked) state — see Effective.
type Forward struct {
	Spec    Spec
	PID     int
	Status  Status
	LogPath string
}

const (
	specFile   = "spec.json"
	pidFile    = "supervisor.pid"
	statusFile = "status.json"
	logFile    = "supervisor.log"
)

// labNamePattern bounds what may be used as the <lab> path component. Lab
// names come from config metadata.name (prefixed "astro-"), which isn't
// otherwise validated — this keeps it from ever escaping BaseDir.
var labNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateLabName(lab string) error {
	if !labNamePattern.MatchString(lab) || strings.Contains(lab, "..") {
		return fmt.Errorf("invalid lab name '%s' for port forward state", lab)
	}
	return nil
}

func validateForwardName(name string) error {
	return config.PortForward{Name: name, Resource: "svc/x", HostPort: 1024, TargetPort: 1}.Validate()
}

// BaseDir returns ~/.astrona/portforward (not created).
func BaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "portforward"), nil
}

func labDir(lab string) (string, error) {
	if err := validateLabName(lab); err != nil {
		return "", err
	}
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, lab), nil
}

func forwardDir(lab, name string) (string, error) {
	if err := validateForwardName(name); err != nil {
		return "", err
	}
	dir, err := labDir(lab)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// writeJSONAtomic writes v to path via a temp file + rename, so a reader
// (`list` polling status.json while the supervisor updates it) never sees a
// half-written file.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", filepath.Base(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return nil
}

// loadSpec reads and re-validates a forward's spec — the supervisor only
// ever acts on a spec that passes the same checks the lab config did.
func loadSpec(dir string) (Spec, error) {
	var s Spec
	if err := readJSON(filepath.Join(dir, specFile), &s); err != nil {
		return s, err
	}
	if err := validateLabName(s.Lab); err != nil {
		return s, err
	}
	if err := s.Forward.Validate(); err != nil {
		return s, err
	}
	if s.KubeContext != "kind-"+s.Lab {
		return s, fmt.Errorf("spec kube context '%s' does not match lab '%s'", s.KubeContext, s.Lab)
	}
	return s, nil
}

func readPID(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, pidFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

func loadForward(dir string) (Forward, error) {
	spec, err := loadSpec(dir)
	if err != nil {
		return Forward{}, err
	}
	f := Forward{Spec: spec, PID: readPID(dir), LogPath: filepath.Join(dir, logFile)}
	if err := readJSON(filepath.Join(dir, statusFile), &f.Status); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Forward{}, err
		}
		f.Status = Status{State: StateNotReady, Since: spec.StartedAt}
	}
	return f, nil
}

// List returns every forward on disk, for lab only when lab is non-empty,
// sorted by lab then name. Unreadable entries are skipped — a corrupt state
// dir shouldn't hide the rest.
func List(lab string) ([]Forward, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}

	var labs []string
	if lab != "" {
		if err := validateLabName(lab); err != nil {
			return nil, err
		}
		labs = []string{lab}
	} else {
		entries, err := os.ReadDir(base)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("failed to read port forward state dir '%s': %w", base, err)
		}
		for _, e := range entries {
			if e.IsDir() && validateLabName(e.Name()) == nil {
				labs = append(labs, e.Name())
			}
		}
	}

	var out []Forward
	for _, l := range labs {
		entries, err := os.ReadDir(filepath.Join(base, l))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read port forward state for lab '%s': %w", l, err)
		}
		for _, e := range entries {
			if !e.IsDir() || validateForwardName(e.Name()) != nil {
				continue
			}
			f, err := loadForward(filepath.Join(base, l, e.Name()))
			if err != nil {
				continue
			}
			out = append(out, f)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Spec.Lab != out[j].Spec.Lab {
			return out[i].Spec.Lab < out[j].Spec.Lab
		}
		return out[i].Spec.Forward.Name < out[j].Spec.Forward.Name
	})
	return out, nil
}

// Count returns how many forwards are recorded for lab (any state).
func Count(lab string) int {
	fs, err := List(lab)
	if err != nil {
		return 0
	}
	return len(fs)
}
