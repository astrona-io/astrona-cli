// Package telemetry keeps the CLI's opt-out usage analytics: small events
// about the labs it runs (a run started or finished, the lab clock started,
// a submit, a teardown or an abandoned run, a workspace tab opened or
// closed, the update notice shown), collected only to improve the Astrona
// platform and its labs.
//
// An event carries the catalog lab name (never a local path), lab or
// playground, the runtime target, durations, pass or fail with check
// counts, an error class from a fixed list, a random run id, and the CLI
// version, OS, architecture and container runtime with its major version.
// It never carries a host name, user name, path, command line, environment,
// file content, IP address or the text of an error.
//
// Every batch is sent with a random install id (a UUID v4 in
// ~/.astrona/telemetry/install-id, made on first use and never derived from
// the hardware or the user). Unlike the guest-run id (internal/guestrun) it
// is not replaced on login or logout; `astrona telemetry reset` replaces it.
//
// Events wait in ~/.astrona/telemetry/spool.jsonl (bounded) until they are
// sent; nothing here touches the network or prints. It is on by default:
// the "telemetry" setting, ASTRONA_TELEMETRY=0 and DO_NOT_TRACK=1 turn it
// off, and turning it off deletes what waits.
package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/filelock"
	"astrona/internal/settings"
)

// PrivacyURL is where the data and the opt-out are described.
const PrivacyURL = "https://astrona.io/privacy#cli-usage-data"

// Event types, as the site takes them.
const (
	RunStarted   = "run_started"
	RunFinished  = "run_finished"
	TimerStarted = "timer_started"
	Submit       = "submit"
	Teardown     = "teardown"
	RunAbandoned = "run_abandoned"
	TabOpened    = "tab_opened"
	TabClosed    = "tab_closed"
	UpdateShown  = "update_shown"
)

// Error classes: the only thing about a failure that is sent.
const (
	ErrEngineUnreachable     = "engine_unreachable"
	ErrInsufficientResources = "insufficient_resources"
	ErrImagePull             = "image_pull"
	ErrTimeout               = "timeout"
	ErrReadiness             = "readiness"
	ErrProvision             = "provision"
	ErrPortConflict          = "port_conflict"
	ErrInterrupted           = "interrupted"
	ErrNetwork               = "network"
	ErrGrading               = "grading"
	ErrOther                 = "other"
)

// Limits shared with the site.
const (
	// MaxBatch is the most events one POST carries.
	MaxBatch = 100
	// maxSpool is the most events kept waiting; the oldest go first.
	maxSpool = 500
	// maxAge is how long an event waits before it is dropped (the site
	// refuses events older than 30 days).
	maxAge = 29 * 24 * time.Hour
	// maxSeconds caps a duration (the site refuses more than a day).
	maxSeconds = 86400
)

// Event is one usage event. Optional fields are omitted when unset.
type Event struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	At               string `json:"at"`
	RunID            string `json:"run_id,omitempty"`
	Lab              string `json:"lab,omitempty"`
	Kind             string `json:"kind,omitempty"`
	Target           string `json:"target,omitempty"`
	Outcome          string `json:"outcome,omitempty"`
	ProvisionSeconds *int   `json:"provision_seconds,omitempty"`
	ReadySeconds     *int   `json:"ready_seconds,omitempty"`
	TeardownSeconds  *int   `json:"teardown_seconds,omitempty"`
	SessionSeconds   *int   `json:"session_seconds,omitempty"`
	ChecksTotal      *int   `json:"checks_total,omitempty"`
	ChecksPassed     *int   `json:"checks_passed,omitempty"`
	Attempt          int    `json:"attempt,omitempty"`
	ErrorClass       string `json:"error_class,omitempty"`
	Tab              string `json:"tab,omitempty"`
	DocIndex         *int   `json:"doc_index,omitempty"`
	CLIVersion       string `json:"cli_version"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	Runtime          string `json:"runtime,omitempty"`
	RuntimeMajor     *int   `json:"runtime_major,omitempty"`
}

// Spooled is an event waiting to be sent, with whether it was recorded
// while signed in (not sent: it picks the route).
type Spooled struct {
	Signed bool  `json:"signed"`
	Event  Event `json:"event"`
}

var (
	mu sync.Mutex
	// version is this build's version (Configure).
	version = "developer"
	// settingsFile overrides the settings path (--settings-file).
	settingsFile string
	// SignedIn reports, from local credentials only, whether the CLI is
	// signed in to the site it sends to. labops sets it.
	SignedIn = func() bool { return false }
)

// Configure sets this build's version and the settings file the "telemetry"
// setting is read from ("" is the default one).
func Configure(buildVersion, settingsPath string) {
	mu.Lock()
	defer mu.Unlock()
	version, settingsFile = buildVersion, settingsPath
}

// Seconds is d as whole seconds within the site's range.
func Seconds(d time.Duration) *int {
	s := int(d / time.Second)
	s = max(0, min(s, maxSeconds))
	return &s
}

// Int is a pointer to n, for the optional counts.
func Int(n int) *int { return &n }

// State says whether telemetry is on and, when off, what turned it off.
type State struct {
	Enabled bool
	// Reason is why it is off: "setting", "ASTRONA_TELEMETRY" or
	// "DO_NOT_TRACK"; "" when on.
	Reason string
}

// envOff reports whether an environment variable turns telemetry off.
func envOff(getenv func(string) string) string {
	switch strings.ToLower(strings.TrimSpace(getenv("ASTRONA_TELEMETRY"))) {
	case "0", "false", "off", "no":
		return "ASTRONA_TELEMETRY"
	}
	if v := strings.TrimSpace(getenv("DO_NOT_TRACK")); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return "DO_NOT_TRACK"
	}
	return ""
}

// Current is whether events are recorded and sent now. A settings file that
// cannot be read counts as off: when in doubt, send nothing.
func Current() State {
	if reason := envOff(os.Getenv); reason != "" {
		return State{Reason: reason}
	}
	mu.Lock()
	file := settingsFile
	mu.Unlock()
	path, err := settings.Path(file)
	if err != nil {
		return State{Reason: "setting"}
	}
	saved, err := settings.Load(path)
	if err != nil {
		return State{Reason: "setting"}
	}
	value, err := saved.Value("telemetry")
	if err != nil || string(value) != "true" {
		return State{Reason: "setting"}
	}
	return State{Enabled: true}
}

// enabled is Current().Enabled, and never in a test binary: tests must not
// write to the real home or reach a site.
func enabled() bool {
	return !testing.Testing() && Current().Enabled
}

// Dir is ~/.astrona/telemetry.
func Dir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, apphome.Name(), "telemetry"), nil
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// NewID is a random UUID v4.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// InstallID is the random install id, made the first time it is needed. An
// unreadable or malformed one is replaced.
func InstallID() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(filepath.Join(dir, "install-id")); err == nil {
		if id := strings.TrimSpace(string(data)); uuidV4.MatchString(id) {
			return id, nil
		}
	}
	return ResetInstallID()
}

// PeekInstallID is the install id if one exists, without making one.
func PeekInstallID() string {
	dir, err := Dir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "install-id"))
	if id := strings.TrimSpace(string(data)); err == nil && uuidV4.MatchString(id) {
		return id
	}
	return ""
}

// ResetInstallID replaces the install id with a fresh random one. Events
// already waiting go out with the new one.
func ResetInstallID() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	id := NewID()
	if err := writeAtomic(filepath.Join(dir, "install-id"), []byte(id+"\n")); err != nil {
		return "", fmt.Errorf("save the install id: %w", err)
	}
	return id, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Notice is the one-time notice printed (to stderr) the first time the CLI
// runs with telemetry on. Purpose first, then what is sent, then opt-out.
const Notice = `The astrona CLI sends usage data only to improve the Astrona platform and its labs:
which labs are used, how long they take, and where people get stuck or hit errors.
Signed out, events carry a random install id made on this computer; signed in, they are linked to your account.
Never sent: your files, file paths, commands, command lines, host or user names, or your IP address.
Turn it off with ` + "`astrona telemetry off`" + `, or set ASTRONA_TELEMETRY=0 or DO_NOT_TRACK=1.
` + "`astrona telemetry status`" + ` shows exactly what is sent. Details: ` + PrivacyURL + `
`

// NoticeShown reports whether the first-run notice was shown.
func NoticeShown() bool {
	dir, err := Dir()
	if err != nil {
		return true // nowhere to remember it: don't print it on every command
	}
	_, err = os.Stat(filepath.Join(dir, "notice-shown"))
	return err == nil
}

// MarkNoticeShown remembers that the first-run notice was shown.
func MarkNoticeShown() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "notice-shown"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
}

func osName() string {
	switch goruntime.GOOS {
	case "linux", "darwin", "windows":
		return goruntime.GOOS
	}
	return "other"
}

func archName() string {
	switch goruntime.GOARCH {
	case "amd64", "arm64":
		return goruntime.GOARCH
	}
	return "other"
}

var versionText = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

func cliVersion() string {
	mu.Lock()
	v := version
	mu.Unlock()
	v = versionText.ReplaceAllString(v, "")
	if len(v) > 32 {
		v = v[:32]
	}
	if v == "" {
		v = "unknown"
	}
	return v
}

// Record adds ev to the spool when telemetry is on, filling its id, time and
// the CLI's version, OS and architecture. Best effort: an error is dropped,
// so telemetry never fails a command.
func Record(ev Event) {
	if !enabled() {
		return
	}
	ev.ID = NewID()
	ev.At = time.Now().UTC().Format(time.RFC3339Nano)
	ev.CLIVersion, ev.OS, ev.Arch = cliVersion(), osName(), archName()
	_ = appendSpool(Spooled{Signed: SignedIn(), Event: ev})
}

func spoolPaths() (spool, lock string, err error) {
	dir, err := Dir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "spool.jsonl"), filepath.Join(dir, "spool.lock"), nil
}

func lockSpool(ctx context.Context) (spool string, release func(), err error) {
	spool, lock, err := spoolPaths()
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	release, err = filelock.Wait(ctx, lock)
	return spool, release, err
}

func appendSpool(s Spooled) error {
	spool, release, err := lockSpool(context.Background())
	if err != nil {
		return err
	}
	defer release()
	waiting := readSpool(spool)
	waiting = append(waiting, s)
	return writeSpool(spool, prune(waiting, time.Now()))
}

// readSpool reads the waiting events; unreadable lines are dropped.
func readSpool(path string) []Spooled {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Spooled
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		var s Spooled
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Event.ID != "" {
			out = append(out, s)
		}
	}
	return out
}

func writeSpool(path string, waiting []Spooled) error {
	if len(waiting) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	var buf bytes.Buffer
	for _, s := range waiting {
		line, err := json.Marshal(s)
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(path, buf.Bytes())
}

// prune drops events too old (or too far ahead) for the site, then the
// oldest beyond maxSpool.
func prune(waiting []Spooled, now time.Time) []Spooled {
	kept := waiting[:0]
	for _, s := range waiting {
		at, err := time.Parse(time.RFC3339Nano, s.Event.At)
		if err != nil || now.Sub(at) > maxAge || at.Sub(now) > time.Minute {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) > maxSpool {
		kept = kept[len(kept)-maxSpool:]
	}
	return kept
}

// Pending is how many events wait to be sent.
func Pending() int {
	spool, _, err := spoolPaths()
	if err != nil {
		return 0
	}
	return len(readSpool(spool))
}

// Clear deletes every waiting event and the runs being followed (after
// `astrona telemetry off`).
func Clear() error {
	spool, release, err := lockSpool(context.Background())
	if err != nil {
		return err
	}
	defer release()
	if err := os.Remove(spool); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, "runs"))
}

// Poster sends one batch for installID: signed picks the signed-in route.
type Poster func(ctx context.Context, signed bool, installID string, events []Event) error

// Flush sends the waiting events in batches of at most MaxBatch, signed-in
// and guest events apart. A batch the site will never take (permanent) is
// dropped; any other failure keeps the rest for later. It returns how many
// are left. With telemetry off it deletes what waits instead.
func Flush(ctx context.Context, post Poster, permanent func(error) bool) (int, error) {
	if !enabled() {
		if !testing.Testing() {
			_ = Clear()
		}
		return 0, nil
	}
	spool, release, err := lockSpool(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	waiting := prune(readSpool(spool), time.Now())
	if len(waiting) == 0 {
		return 0, writeSpool(spool, nil)
	}
	installID, err := InstallID()
	if err != nil {
		return len(waiting), err
	}
	var sendErr error
	for _, signed := range []bool{false, true} {
		for sendErr == nil {
			var batch []Event
			var rest []Spooled
			for _, s := range waiting {
				if s.Signed == signed && len(batch) < MaxBatch {
					batch = append(batch, s.Event)
				} else {
					rest = append(rest, s)
				}
			}
			if len(batch) == 0 {
				break
			}
			if err := post(ctx, signed, installID, batch); err != nil && !permanent(err) {
				sendErr = err
				break
			}
			waiting = rest
		}
	}
	if err := writeSpool(spool, waiting); err != nil && sendErr == nil {
		sendErr = err
	}
	return len(waiting), sendErr
}

// RecentFailure reports whether a send failed in the last d, so a command
// does not wait on an unreachable site again (the background sender
// retries).
func RecentFailure(d time.Duration) bool {
	dir, err := Dir()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "last-failure"))
	return err == nil && time.Since(info.ModTime()) < d
}

// NoteFailure records that a send failed now; ok clears it.
func NoteFailure(failed bool) {
	dir, err := Dir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "last-failure")
	if !failed {
		_ = os.Remove(path)
		return
	}
	_ = writeAtomic(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
}

// Run is what is remembered about one `astrona run` while its lab exists,
// so later commands (submit, destroy, the browser workspace) can link
// their events to it. It holds no path and no name but the catalog lab's.
type Run struct {
	RunID        string    `json:"run_id"`
	Lab          string    `json:"lab,omitempty"`
	Kind         string    `json:"kind"`
	Target       string    `json:"target"`
	Runtime      string    `json:"runtime,omitempty"`
	RuntimeMajor *int      `json:"runtime_major,omitempty"`
	Started      time.Time `json:"started"`
	Attempts     int       `json:"attempts"`
}

// Event is a new event of type kind for this run.
func (r *Run) Event(kind string) Event {
	return Event{
		Type: kind, RunID: r.RunID, Lab: r.Lab, Kind: r.Kind, Target: r.Target,
		Runtime: r.Runtime, RuntimeMajor: r.RuntimeMajor,
	}
}

// runPath is the run file of a lab, named by a hash of the cluster name.
func runPath(cluster string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(cluster))
	return filepath.Join(dir, "runs", hex.EncodeToString(sum[:16])+".json"), nil
}

// StartRun remembers r as cluster's current run when telemetry is on, and
// returns it (nil when off).
func StartRun(cluster string, r Run) *Run {
	if !enabled() {
		return nil
	}
	r.RunID = NewID()
	r.Started = time.Now().UTC()
	if err := SaveRun(cluster, &r); err != nil {
		return nil
	}
	return &r
}

// SaveRun writes cluster's run file.
func SaveRun(cluster string, r *Run) error {
	path, err := runPath(cluster)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// LoadRun is cluster's current run, or nil (none, unreadable, or telemetry
// off).
func LoadRun(cluster string) *Run {
	if !enabled() {
		return nil
	}
	path, err := runPath(cluster)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r Run
	if json.Unmarshal(data, &r) != nil || !uuidV4.MatchString(r.RunID) {
		return nil
	}
	return &r
}

// EndRun forgets cluster's run.
func EndRun(cluster string) {
	if path, err := runPath(cluster); err == nil {
		_ = os.Remove(path)
	}
}

// once remembers, in this process, the tab events already recorded per run,
// so a page reload does not count a tab twice.
var once = struct {
	sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

// RecordTab records a tab_opened or tab_closed event for cluster's run, at
// most once per run, kind, tab name and doc link in this process. docIndex is
// the doc link's position (doc_link tabs only).
func RecordTab(cluster, kind, tab, name string, docIndex *int) {
	r := LoadRun(cluster)
	if r == nil {
		return
	}
	key := r.RunID + "\x00" + kind + "\x00" + tab + "\x00" + name
	if docIndex != nil {
		key += fmt.Sprintf("\x00%d", *docIndex)
	}
	once.Lock()
	seen := once.seen[key]
	once.seen[key] = true
	once.Unlock()
	if seen {
		return
	}
	ev := r.Event(kind)
	ev.Tab, ev.DocIndex = tab, docIndex
	Record(ev)
}
