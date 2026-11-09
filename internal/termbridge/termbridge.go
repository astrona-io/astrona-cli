// Package termbridge runs one lab terminal in a pseudo-terminal and lets a
// viewer (the browser workspace) attach to it, detach and attach again
// without touching the process.
//
// Security contract:
//
//   - The bridge never builds its own command. Start takes an *exec.Cmd the
//     caller built through one of the existing terminal factories (the
//     hardened Kind container from labshell.Command, or the student SSH
//     login). It does not look at, rewrite or wrap that command.
//   - There is no fallback. If the command cannot start, Start returns the
//     error and nothing else runs: never a host shell, never a retry with a
//     different program.
//   - Memory is bounded. Output is kept in a ring buffer of a fixed size
//     (1 MiB unless Options says otherwise); older output is dropped. A viewer
//     that falls further behind than the buffer is detached with ErrOverrun
//     instead of growing a queue.
//   - One writer. Only the most recently attached Client may write input or
//     resize the terminal. A new Attach takes over and the previous Client
//     gets ErrTakenOver, so two tabs never type into the same shell.
//   - Clean shutdown. When the process exits, or Close ends it, the bridge
//     reaps the process, runs the command's Cancel hook (which removes an
//     isolated terminal container), closes the PTY and stops every goroutine
//     it started before Done is closed.
//
// The package knows nothing about HTTP or WebSockets; the workspace server
// adapts a Client to its own transport.
package termbridge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// DefaultBufferSize is the replay buffer kept per terminal.
	DefaultBufferSize = 1 << 20
	// MaxBufferSize caps Options.BufferSize so one terminal cannot be
	// configured into an unbounded allocation.
	MaxBufferSize = 64 << 20
	// MaxDimension is the largest accepted column or row count.
	MaxDimension = 1000

	readChunk = 32 << 10
	// closeGrace is how long Close waits after hanging up the terminal
	// before it kills the process.
	closeGrace = 3 * time.Second
	// drainTimeout is how long output is still read after the process
	// exited, for output that was in flight when it ended.
	drainTimeout = 500 * time.Millisecond
)

var (
	// ErrUnsupported is returned by Start where no PTY is available
	// (native Windows; there the CLI runs inside WSL).
	ErrUnsupported = errors.New("lab terminals are not supported on native Windows; run astrona inside WSL")
	// ErrTakenOver is returned to a Client after another Attach replaced it.
	ErrTakenOver = errors.New("terminal opened in another view")
	// ErrDetached is returned by a Client after its own Close.
	ErrDetached = errors.New("terminal view detached")
	// ErrOverrun is returned when a Client fell behind by more than the
	// buffer; the Client is detached and the caller may Attach again.
	ErrOverrun = errors.New("terminal view fell behind the output buffer")
	// ErrEnded is returned by Write and Resize after the process ended.
	ErrEnded = errors.New("terminal ended")
)

// Options configure Start. The zero value is valid.
type Options struct {
	// BufferSize is the replay buffer in bytes; 0 means DefaultBufferSize.
	BufferSize int
	// Cols and Rows are the initial size; 0 means 80x24.
	Cols, Rows int
}

// Session is one running terminal: a process on a PTY plus its recent output.
type Session struct {
	cmd    *exec.Cmd
	master *os.File

	mu      sync.Mutex
	cond    *sync.Cond
	ring    ring
	total   int // bytes ever written to the ring (64-bit on every release target)
	current *Client
	ended   bool  // process reaped and output drained
	exitErr error // cmd.Wait's result

	closeOnce sync.Once
	done      chan struct{}
}

// Client is one attached view. Read returns live output, Write sends input.
// After it is replaced, detached or the terminal ends, it stays inert.
type Client struct {
	s      *Session
	pos    int   // next byte to read, as an offset into the output stream
	reason error // why the client stopped; nil while attached
}

// Start runs cmd on a new PTY. cmd must be built by the caller and not yet
// started; its Stdin, Stdout and Stderr must be unset because the PTY
// provides them. A start failure is returned as is: there is no fallback.
func Start(cmd *exec.Cmd, opts Options) (*Session, error) {
	if cmd == nil {
		return nil, errors.New("start terminal: no command")
	}
	if cmd.Process != nil {
		return nil, errors.New("start terminal: command already started")
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, errors.New("start terminal: command already has its own stdin, stdout or stderr")
	}
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupported
	}
	size := opts.BufferSize
	if size == 0 {
		size = DefaultBufferSize
	}
	if size < 0 || size > MaxBufferSize {
		return nil, fmt.Errorf("start terminal: buffer size must be between 1 and %d bytes", MaxBufferSize)
	}
	cols, rows := opts.Cols, opts.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ws, err := winsize(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	master, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		if errors.Is(err, pty.ErrUnsupported) {
			return nil, ErrUnsupported
		}
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	// A pollable PTY lets the bridge stop its reader with a deadline even
	// when another process still holds the terminal open.
	polled, err := pollable(master)
	if err != nil {
		_ = master.Close()
		s := &Session{cmd: cmd}
		s.kill()
		_ = cmd.Wait()
		s.cancel()
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	s := &Session{
		cmd:    cmd,
		master: polled,
		ring:   ring{buf: make([]byte, size)},
		done:   make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	pumped := make(chan struct{})
	go s.pump(pumped)
	go s.wait(pumped)
	return s, nil
}

// pump copies PTY output into the ring until the PTY reports an error:
// end of file, hang-up (EIO on Linux) or the deadline set by wait.
func (s *Session) pump(pumped chan<- struct{}) {
	defer close(pumped)
	buf := make([]byte, readChunk)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.ring.write(buf[:n])
			s.total += n
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// wait reaps the process, runs its Cancel hook, drains what is left of
// the output, closes the PTY and then reports the end.
func (s *Session) wait(pumped <-chan struct{}) {
	err := s.cmd.Wait()
	// labshell.Command's Cancel removes the terminal container; callers
	// must run it after Wait too, not only on context cancellation.
	s.cancel()
	select {
	case <-pumped:
	case <-time.After(drainTimeout):
		_ = s.master.SetReadDeadline(time.Now())
		<-pumped
	}
	_ = s.master.Close()

	s.mu.Lock()
	s.ended = true
	s.exitErr = err
	s.cond.Broadcast()
	s.mu.Unlock()
	close(s.done)
}

func (s *Session) cancel() {
	if s.cmd.Cancel != nil {
		_ = s.cmd.Cancel()
	}
}

func (s *Session) kill() {
	if s.cmd.Cancel != nil {
		_ = s.cmd.Cancel()
	} else if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// Attach makes a new Client the only attached view and returns it with a
// copy of the buffered output. Live output after that copy is read from the
// Client. A previously attached Client gets ErrTakenOver. Attaching after
// the terminal ended is allowed and returns the final output.
func (s *Session) Attach() (*Client, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		s.current.reason = ErrTakenOver
	}
	c := &Client{s: s, pos: s.total}
	s.current = c
	s.cond.Broadcast()
	return c, s.ring.snapshot()
}

// Attached reports whether a Client is currently attached.
func (s *Session) Attached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != nil
}

// Done is closed once the process has been reaped, its Cancel hook has run,
// the PTY is closed and the bridge's goroutines have stopped.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is the process's exit result (nil for exit status 0, otherwise
// usually an *exec.ExitError). It is only meaningful after Done is closed.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// Close ends the terminal: it hangs up the process, kills it after a short
// grace period, and waits until Done. It is safe to call more than once and
// after the process already ended.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		select {
		case <-s.done:
			return
		default:
		}
		_ = s.cmd.Process.Signal(syscall.SIGHUP)
		select {
		case <-s.done:
		case <-time.After(closeGrace):
			s.kill()
		}
	})
	<-s.done
	return nil
}

// Read returns output written after the Client attached. It blocks until
// there is output, returns io.EOF once the terminal ended and everything
// was read, and returns ErrTakenOver, ErrDetached or ErrOverrun when the
// Client is no longer attached.
func (c *Client) Read(p []byte) (int, error) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if c.reason != nil {
			return 0, c.reason
		}
		if c.pos < s.total-s.ring.filled {
			c.detach(ErrOverrun)
			return 0, c.reason
		}
		if c.pos < s.total {
			if len(p) == 0 {
				return 0, nil
			}
			n := s.ring.readAt(c.pos-(s.total-s.ring.filled), p)
			c.pos += n
			return n, nil
		}
		if s.ended {
			return 0, io.EOF
		}
		s.cond.Wait()
	}
}

// Write sends input to the terminal. Only the attached Client may write.
func (c *Client) Write(p []byte) (int, error) {
	if err := c.usable(); err != nil {
		return 0, err
	}
	return c.s.master.Write(p)
}

// Resize sets the terminal size. Only the attached Client may resize.
func (c *Client) Resize(cols, rows int) error {
	ws, err := winsize(cols, rows)
	if err != nil {
		return err
	}
	if err := c.usable(); err != nil {
		return err
	}
	return pty.Setsize(c.s.master, ws)
}

// Close detaches the Client. The process keeps running.
func (c *Client) Close() error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.reason == nil {
		c.detach(ErrDetached)
	}
	return nil
}

// detach must be called with s.mu held.
func (c *Client) detach(reason error) {
	c.reason = reason
	if c.s.current == c {
		c.s.current = nil
	}
	c.s.cond.Broadcast()
}

func (c *Client) usable() error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.reason != nil {
		return c.reason
	}
	if c.s.ended {
		return ErrEnded
	}
	return nil
}

func winsize(cols, rows int) (*pty.Winsize, error) {
	if cols < 1 || cols > MaxDimension || rows < 1 || rows > MaxDimension {
		return nil, fmt.Errorf("terminal size %dx%d is outside 1..%d", cols, rows, MaxDimension)
	}
	return &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}, nil
}

// ring keeps the last len(buf) bytes written to it.
type ring struct {
	buf    []byte
	head   int // next write position
	filled int // bytes held, at most len(buf)
}

func (r *ring) write(p []byte) {
	size := len(r.buf)
	if len(p) >= size {
		copy(r.buf, p[len(p)-size:])
		r.head, r.filled = 0, size
		return
	}
	n := copy(r.buf[r.head:], p)
	copy(r.buf, p[n:])
	r.head = (r.head + len(p)) % size
	r.filled = min(r.filled+len(p), size)
}

// readAt copies held bytes starting off bytes after the oldest one.
func (r *ring) readAt(off int, p []byte) int {
	size := len(r.buf)
	start := (r.head - r.filled + off + 2*size) % size
	want := min(len(p), r.filled-off)
	n := copy(p[:want], r.buf[start:])
	if n < want {
		n += copy(p[n:want], r.buf)
	}
	return n
}

func (r *ring) snapshot() []byte {
	out := make([]byte, r.filled)
	r.readAt(0, out)
	return out
}
