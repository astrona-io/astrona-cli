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

// stepQueue sends build steps to the page in order, off the build's
// goroutine: a slow or unreachable site never holds the build up, and a step
// that doesn't fit the queue is dropped.
//
// It also keeps a long step alive: labs fails a preparing session (or exam)
// that sends nothing for a while (its stall sweep), and one step — a
// 30-minute waitFor, a slow image pull — can last longer than that. While
// the queue is open, the last step is sent again whenever nothing was sent
// for ProgressKeepalive. Labs takes at most 200 progress events per session
// and 60 a minute; one every five minutes stays far inside both.
type stepQueue struct {
	mu     sync.Mutex
	steps  chan progressStep
	done   chan struct{}
	closed bool
	send   func(progressStep)

	lastMu   sync.Mutex
	last     progressStep
	lastSent time.Time
}

// progressStep is one line for the page. Task, when set, is the exam task
// whose setup the line reports as applied.
type progressStep struct {
	Message string
	Task    int
}

const (
	stepQueueSize = 32
	// stepFlushWait bounds how long ready or failed waits for the steps
	// before it, so they reach the page first.
	stepFlushWait = 10 * time.Second
)

// ProgressKeepalive is how long a step may go without being sent again
// while it lasts (see stepQueue). Labs' stall sweep is 45 minutes for a
// lab session and 15 for a lab exam's preparation.
var ProgressKeepalive = 5 * time.Minute

// newStepQueue starts a queue that hands each step to send.
func newStepQueue(send func(progressStep)) *stepQueue {
	q := &stepQueue{steps: make(chan progressStep, stepQueueSize), done: make(chan struct{}), send: send}
	go q.run()
	go q.keepAlive()
	return q
}

// add queues step, unless the queue is full or closed.
func (q *stepQueue) add(step progressStep) {
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

func (q *stepQueue) run() {
	defer close(q.done)
	for step := range q.steps {
		q.send(step)
		q.lastMu.Lock()
		q.last, q.lastSent = step, time.Now()
		q.lastMu.Unlock()
	}
}

// keepAlive sends the last step again while nothing new was sent for
// ProgressKeepalive, until the queue closes.
func (q *stepQueue) keepAlive() {
	tick := ProgressKeepalive / 5
	if tick <= 0 {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-q.done:
			return
		case <-t.C:
			q.lastMu.Lock()
			last, idle := q.last, time.Since(q.lastSent)
			q.lastMu.Unlock()
			if last.Message != "" && idle >= ProgressKeepalive {
				q.add(last)
			}
		}
	}
}

// close stops taking steps and waits, briefly, for the queued ones.
func (q *stepQueue) close() {
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
		ls.queue = newStepQueue(func(step progressStep) { ls.Progress("preparing", step.Message) })
	}
	q := ls.queue
	ls.stepsMu.Unlock()
	q.add(progressStep{Message: step})
}

// flushSteps stops taking steps and waits, briefly, for the queued ones.
func (ls *LabStart) flushSteps() {
	ls.stepsMu.Lock()
	q := ls.queue
	ls.stepsMu.Unlock()
	if q != nil {
		q.close()
	}
}
