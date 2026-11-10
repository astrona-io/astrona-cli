package labops

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"astrona/internal/ui"
)

// What the lab page shows while the lab builds: the build's sections, as a
// student reads them, and each linked cluster once it is ready. A lab with
// several clusters takes many minutes; without these the page would say
// "Preparing" from start to end.
var (
	linkedSection = regexp.MustCompile(`^Linked cluster '([^']+)'$`)
	linkedReady   = regexp.MustCompile(`^Linked cluster '([^']+)' ready`)
	linkedBatch   = regexp.MustCompile(`^Linked clusters \(up to (\d+) at once\)$`)
)

var sectionSteps = map[string]string{
	"Images":        "Loading images into the cluster",
	"Addons":        "Installing add-ons",
	"Port forwards": "Starting port forwards",
	"Bootstrap":     "Running the lab's setup scripts",
	"Manifests":     "Applying the lab's manifests",
	"Readiness":     "Waiting for the lab to become ready",
}

// buildStep is the page's line for a build event, or "" for one it skips.
func buildStep(e ui.Event) string {
	label := strings.TrimSpace(e.Label)
	switch e.Kind {
	case "section":
		if step, ok := sectionSteps[label]; ok {
			return step
		}
		if m := linkedSection.FindStringSubmatch(label); m != nil {
			return "Creating linked cluster " + m[1]
		}
		if m := linkedBatch.FindStringSubmatch(label); m != nil {
			if m[1] == "1" {
				return "Creating linked clusters"
			}
			return "Creating linked clusters, " + m[1] + " at a time"
		}
		if strings.HasPrefix(label, "Lab: ") {
			return "Creating the lab's cluster"
		}
	case "ok":
		if m := linkedReady.FindStringSubmatch(label); m != nil {
			return "Linked cluster " + m[1] + " is ready"
		}
	}
	return ""
}

// stepQueue sends build steps to the lab page in order, off the build's
// goroutine: a slow or unreachable site never holds the build up, and a step
// that doesn't fit the queue is dropped.
type stepQueue struct {
	mu     sync.Mutex
	steps  chan string
	done   chan struct{}
	closed bool
}

const (
	stepQueueSize = 32
	// stepFlushWait bounds how long ready or failed waits for the steps
	// before it, so they reach the page first.
	stepFlushWait = 10 * time.Second
)

// Observe is a ui.Reporter observer: it queues the page's line for e, if any.
func (ls *LabStart) Observe(e ui.Event) {
	if ls == nil || ls.Session == nil || ls.Session.IsPlayground() {
		return
	}
	step := buildStep(e)
	if step == "" {
		return
	}
	ls.stepsMu.Lock()
	if ls.queue == nil {
		ls.queue = &stepQueue{steps: make(chan string, stepQueueSize), done: make(chan struct{})}
		go ls.sendSteps(ls.queue)
	}
	q := ls.queue
	ls.stepsMu.Unlock()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	select {
	case q.steps <- step:
	default:
	}
}

func (ls *LabStart) sendSteps(q *stepQueue) {
	defer close(q.done)
	for step := range q.steps {
		ls.Progress("preparing", step)
	}
}

// flushSteps stops taking steps and waits, briefly, for the queued ones.
func (ls *LabStart) flushSteps() {
	ls.stepsMu.Lock()
	q := ls.queue
	ls.stepsMu.Unlock()
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.steps)
	}
	q.mu.Unlock()
	select {
	case <-q.done:
	case <-time.After(stepFlushWait):
	}
}
