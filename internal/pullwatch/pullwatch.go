// Package pullwatch shows the image pulls a lab's cluster is waiting on while
// a bootstrap step runs. A step like `istioctl install` can sit for minutes on
// a slow connection with nothing on screen; the kubelet's Pulling and Pulled
// events say what it is downloading and for how long, so a slow network looks
// slow instead of dead.
package pullwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Interval is how often the cluster's events are read.
const Interval = 3 * time.Second

// SlowAfter is how long one pull may take before the step warns that the
// connection looks slow.
const SlowAfter = 2 * time.Minute

// Step is where a watcher reports: the running ui.Task.
type Step interface {
	Status(text string)
	Note(format string, a ...any)
}

// Warner prints a warning above the step (ui.Reporter.Warn).
type Warner func(format string, a ...any)

// Watcher follows one lab cluster's pulls across the steps of a bootstrap.
// It only counts pulls that started after it was created.
type Watcher struct {
	kubeContext, kubeconfig string
	since                   time.Time
	warn                    Warner
	read                    func(ctx context.Context) ([]Event, error)

	mu      sync.Mutex
	pulling map[string]time.Time // image -> when its pull started
	done    map[string]bool      // images reported as pulled
	warned  map[string]bool      // images warned about as slow
}

// New watches the cluster behind kubeContext (with kubeconfig, when the lab
// has its own). A watcher with no context does nothing.
func New(kubeContext, kubeconfig string, warn Warner) *Watcher {
	w := &Watcher{
		kubeContext: kubeContext, kubeconfig: kubeconfig, since: time.Now().Add(-5 * time.Second), warn: warn,
		pulling: map[string]time.Time{}, done: map[string]bool{}, warned: map[string]bool{},
	}
	w.read = w.kubectlEvents
	return w
}

// Attach reports pulls on step until the returned stop is called. stop waits
// for the watcher to finish, so the step can be resolved right after it.
func (w *Watcher) Attach(step Step) (stop func()) {
	if w == nil || w.kubeContext == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			events, err := w.read(ctx)
			if err != nil || ctx.Err() != nil {
				continue // the cluster may be busy; the next tick tries again
			}
			w.update(step, events, time.Now())
		}
	}()
	return func() {
		cancel()
		<-finished
		step.Status("")
	}
}

// Event is the part of a Kubernetes event the watcher reads.
type Event struct {
	Reason  string
	Message string
	At      time.Time
}

var (
	pullingRe = regexp.MustCompile(`^Pulling image "([^"]+)"`)
	pulledRe  = regexp.MustCompile(`^Successfully pulled image "([^"]+)" in ([0-9.hmsµ]+)`)
	alreadyRe = regexp.MustCompile(`^Container image "([^"]+)" already present`)
)

func (w *Watcher) update(step Step, events []Event, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	for _, e := range events {
		if e.At.Before(w.since) {
			continue
		}
		switch e.Reason {
		case "Pulling":
			if m := pullingRe.FindStringSubmatch(e.Message); m != nil && !w.done[m[1]] {
				if _, ok := w.pulling[m[1]]; !ok {
					w.pulling[m[1]] = e.At
				}
			}
		case "Pulled":
			if m := pulledRe.FindStringSubmatch(e.Message); m != nil && !w.done[m[1]] {
				took := m[2]
				if d, err := time.ParseDuration(m[2]); err == nil {
					took = Elapsed(d)
				}
				delete(w.pulling, m[1])
				w.done[m[1]] = true
				step.Note("pulled %s in %s", m[1], took)
			} else if m := alreadyRe.FindStringSubmatch(e.Message); m != nil {
				delete(w.pulling, m[1])
			}
		}
	}
	if len(w.pulling) == 0 {
		step.Status("")
		return
	}
	images := make([]string, 0, len(w.pulling))
	for image := range w.pulling {
		images = append(images, image)
	}
	// The longest-running pull first: that is the one to worry about.
	sort.Slice(images, func(i, j int) bool { return w.pulling[images[i]].Before(w.pulling[images[j]]) })
	oldest := images[0]
	took := now.Sub(w.pulling[oldest])
	text := "pulling " + oldest
	if len(images) > 1 {
		text += fmt.Sprintf(" (+%d more)", len(images)-1)
	}
	step.Status(text + " · " + Elapsed(took))
	if took >= SlowAfter && !w.warned[oldest] && w.warn != nil {
		w.warned[oldest] = true
		w.warn("still pulling %s after %s — the download is slow; check your internet connection (VPN, Low Data Mode)", oldest, Elapsed(took))
	}
}

// Elapsed is a short duration: "42s", "2m05s", "1h03m".
func Elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func (w *Watcher) kubectlEvents(ctx context.Context) ([]Event, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "--context", w.kubeContext, "--request-timeout=5s",
		"get", "events", "-A", "-o", "json", "--field-selector", "involvedObject.kind=Pod")
	cmd.Env = os.Environ()
	if w.kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+w.kubeconfig)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseEvents(out)
}

func parseEvents(data []byte) ([]Event, error) {
	var list struct {
		Items []struct {
			Reason         string    `json:"reason"`
			Message        string    `json:"message"`
			FirstTimestamp time.Time `json:"firstTimestamp"`
			LastTimestamp  time.Time `json:"lastTimestamp"`
			EventTime      time.Time `json:"eventTime"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Reason != "Pulling" && item.Reason != "Pulled" {
			continue
		}
		at := item.FirstTimestamp
		if at.IsZero() {
			at = item.EventTime
		}
		if at.IsZero() {
			at = item.LastTimestamp
		}
		events = append(events, Event{Reason: item.Reason, Message: strings.TrimSpace(item.Message), At: at})
	}
	return events, nil
}
