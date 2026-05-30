package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// ShutdownGrace is the time allowed for the subprocess to exit cleanly after
// stdin is closed before the SDK sends SIGTERM.
const ShutdownGrace = 3 * time.Second

// TerminateGrace is the time allowed after SIGTERM before SIGKILL.
const TerminateGrace = 2 * time.Second

// StderrRingSize is the size of the stderr ring buffer captured for
// diagnostic reporting on process errors. 64 KiB is enough for the tail of
// any reasonable crash dump.
const StderrRingSize = 64 * 1024

// AppServerConfig configures the codex app-server subprocess.
type AppServerConfig struct {
	// CLIPath is the absolute path to the codex binary. If empty, FindCLI
	// is used at Connect time.
	CLIPath string

	// ExtraArgs are passed after "app-server". None of the public SDK
	// options map to app-server flags in v0.1.0, but the hook is exposed
	// so integration tests can append --verbose or similar.
	ExtraArgs []string

	// Env is a list of "KEY=VALUE" entries to OVERLAY on top of the parent
	// process environment. A KEY with empty VALUE unsets the variable.
	// OPENAI_API_KEY is passed through from os.Environ by default.
	Env []string

	// Logger is the SDK logger. If nil, a no-op logger is used.
	Logger *sdklog.Logger

	// ReadBufferSize overrides the demux read-buffer ceiling. 0 picks the
	// 2 MiB default.
	ReadBufferSize int

	// Observer receives subprocess-exit and demux telemetry. nil is treated as
	// NopObserver (telemetry dropped). The transport owns OnSubprocessExit; the
	// demux owns the decode/backpressure/first-message events. OnConnect is owned
	// by the Client layer (it spans the full handshake).
	Observer types.Observer

	// MaxConsecutiveParseErrors caps consecutive inbound decode failures before
	// the demux gives up and the transport kills the subprocess. 0 uses the demux
	// default (jsonrpc.DefaultMaxConsecutiveParseErrors).
	MaxConsecutiveParseErrors uint
}

// AppServer is a Transport implementation that spawns `codex app-server`.
type AppServer struct {
	cfg      AppServerConfig
	logger   *sdklog.Logger
	observer types.Observer

	mu          sync.Mutex
	cmd         *exec.Cmd
	stdinW      io.WriteCloser
	demux       *jsonrpc.Demux
	stderr      *ringBuffer
	stderrDone  chan struct{}
	waitDone    chan error
	ready       bool  // true between successful spawn and process exit
	shutdownReq bool  // set by Close before signaling the subprocess
	lastErr     error // first transport-level error (give-up, unexpected exit)
	closedOnce  sync.Once
	connectOnce sync.Once
	exitOnce    sync.Once // guards the single OnSubprocessExit emission
}

// NewAppServer constructs an AppServer transport. It does not spawn the
// subprocess — call Connect.
func NewAppServer(cfg AppServerConfig) *AppServer {
	logger := cfg.Logger
	if logger == nil {
		logger = sdklog.NewLoggerFromZap(nil)
	}
	observer := cfg.Observer
	if observer == nil {
		observer = types.NopObserver{}
	}
	return &AppServer{cfg: cfg, logger: logger, observer: observer}
}

// Connect spawns the subprocess and starts the demux read loop.
func (t *AppServer) Connect(ctx context.Context) error {
	var connErr error
	t.connectOnce.Do(func() { connErr = t.doConnect(ctx) })
	return connErr
}

func (t *AppServer) doConnect(ctx context.Context) error {
	cliPath := t.cfg.CLIPath
	if cliPath == "" {
		p, err := FindCLI()
		if err != nil {
			return err // already a *types.CLINotFoundError
		}
		cliPath = p
	}

	// Soft version probe. Never fails; logs a warning if the version is
	// below RecommendedCLIVersion.
	if v, err := ProbeCLIVersion(cliPath); err == nil {
		recommended, _ := ParseSemVer(RecommendedCLIVersion)
		if !v.AtLeast(recommended) {
			t.logger.Warn("codex CLI version below recommended",
				zap.String("found", v.String()),
				zap.String("recommended", RecommendedCLIVersion))
		} else {
			t.logger.Debug("codex CLI version ok", zap.String("version", v.String()))
		}
	} else {
		t.logger.Warn("could not probe codex CLI version (continuing)",
			zap.String("cli", cliPath), zap.Error(err))
	}

	args := append([]string{"app-server"}, t.cfg.ExtraArgs...)

	// Spawn with a bounded retry on ETXTBSY ("text file busy"). Under heavy concurrent
	// fork/exec, the child of an unrelated spawn can transiently hold a write fd to the
	// target binary in the window between fork and exec, which the kernel reports as
	// ETXTBSY. cmd.Start() consumes its pipes, so the command is rebuilt each attempt.
	// This is the standard mitigation for the Go fork/exec ETXTBSY race.
	var (
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		stdout io.ReadCloser
		stderr io.ReadCloser
	)
	const maxSpawnAttempts = 5
	for attempt := 1; ; attempt++ {
		cmd = exec.CommandContext(ctx, cliPath, args...)
		cmd.Env = buildEnv(t.cfg.Env)

		var pipeErr error
		if stdin, pipeErr = cmd.StdinPipe(); pipeErr != nil {
			return types.NewCLIConnectionError("stdin pipe", pipeErr)
		}
		if stdout, pipeErr = cmd.StdoutPipe(); pipeErr != nil {
			_ = stdin.Close()
			return types.NewCLIConnectionError("stdout pipe", pipeErr)
		}
		if stderr, pipeErr = cmd.StderrPipe(); pipeErr != nil {
			_ = stdin.Close()
			return types.NewCLIConnectionError("stderr pipe", pipeErr)
		}

		startErr := cmd.Start()
		if startErr == nil {
			break
		}
		_ = stdin.Close()
		if errors.Is(startErr, syscall.ETXTBSY) && attempt < maxSpawnAttempts {
			t.logger.Debug("spawn hit ETXTBSY; retrying",
				zap.Int("attempt", attempt), zap.String("cli", cliPath))
			select {
			case <-ctx.Done():
				return types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), ctx.Err())
			case <-time.After(time.Duration(attempt) * 5 * time.Millisecond):
			}
			continue
		}
		return types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), startErr)
	}

	t.mu.Lock()
	t.cmd = cmd
	t.stdinW = stdin
	t.stderr = newRingBuffer(StderrRingSize)
	t.stderrDone = make(chan struct{})
	t.waitDone = make(chan error, 1)
	t.ready = true
	t.mu.Unlock()

	// Stderr drain goroutine — copies into ring buffer for diagnostics.
	go func() {
		defer close(t.stderrDone)
		_, _ = io.Copy(t.stderr, stderr)
	}()

	// Wait goroutine — observes exit for Close() and emits the single
	// OnSubprocessExit telemetry. Exit info is captured under the lock; the
	// Observer is invoked AFTER unlock so it can never block under the mutex.
	go t.watchExit(cmd, t.stderrDone)

	lw := jsonrpc.NewLineWriter(stdin)
	bufSize := t.cfg.ReadBufferSize
	if bufSize < jsonrpc.MinReadBufferSize {
		bufSize = jsonrpc.MinReadBufferSize
	}
	lr := jsonrpc.NewLineReaderWithSize(stdout, bufSize)

	t.demux = jsonrpc.NewDemux(lr, lw, t.logger,
		jsonrpc.WithObserver(t.observer),
		jsonrpc.WithMaxParseErrors(t.cfg.MaxConsecutiveParseErrors),
		jsonrpc.WithUnrecoverableHandler(t.terminateOnUnrecoverableError),
	)
	t.demux.Run(ctx)

	t.logger.Debug("codex app-server spawned",
		zap.String("cli", cliPath),
		zap.Int("pid", cmd.Process.Pid))
	return nil
}

// watchExit blocks on cmd.Wait(), records the exit on waitDone for Close(), and
// emits OnSubprocessExit exactly once. Exit code, requested-flag, and cause are
// captured under the lock; the Observer is invoked after the unlock so it never
// runs under the mutex.
func (t *AppServer) watchExit(cmd *exec.Cmd, stderrDone <-chan struct{}) {
	// cmd.Wait() closes the StderrPipe, so per the os/exec docs all reads from that
	// pipe MUST complete before Wait is called — otherwise Wait races the stderr drain
	// and truncates the captured tail mid-copy (issue 79). The process's stderr
	// write-end closes on exit, the drain goroutine reads to EOF and closes stderrDone,
	// and only then is it safe to reap. On the SIGKILL path the kernel closes stderr
	// too, so the drain still reaches EOF and this never deadlocks.
	if stderrDone != nil {
		<-stderrDone
	}
	waitErr := cmd.Wait()
	t.waitDone <- waitErr

	t.mu.Lock()
	t.ready = false
	requested := t.shutdownReq
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	var cause error
	if !requested && waitErr != nil {
		// Unexpected death (the process exited on its own, not via Close). Record
		// it as the transport error (first error wins — a prior give-up error
		// takes precedence).
		if t.lastErr == nil {
			t.lastErr = types.NewProcessError(
				"codex app-server exited unexpectedly: "+waitErr.Error(),
				exitCode,
				t.stderrLocked(),
			)
		}
		cause = t.lastErr
	} else if !requested {
		// Clean self-exit (waitErr == nil) that we did not request.
		cause = t.lastErr
	}
	t.mu.Unlock()

	t.exitOnce.Do(func() {
		t.observer.OnSubprocessExit(exitCode, requested, cause)
	})
}

// terminateOnUnrecoverableError is the demux give-up callback. It records the
// terminal error (first error wins) and kills the subprocess so a parse give-up
// does not leave a zombie. The watchExit goroutine then observes the death and
// emits OnSubprocessExit. Safe to call from the demux read goroutine; Kill is
// idempotent.
func (t *AppServer) terminateOnUnrecoverableError(reason error) {
	t.mu.Lock()
	if t.lastErr == nil {
		t.lastErr = reason
	}
	var proc *os.Process
	if t.cmd != nil {
		proc = t.cmd.Process
	}
	t.mu.Unlock()

	if proc != nil {
		_ = proc.Kill()
	}
}

// Demux returns the underlying demux. Valid between Connect and Close.
func (t *AppServer) Demux() *jsonrpc.Demux {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.demux
}

// Pid returns the subprocess pid, or 0 if not running.
func (t *AppServer) Pid() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

// Stderr returns the captured stderr tail. Stable after Close.
func (t *AppServer) Stderr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stderrLocked()
}

// stderrLocked returns the captured stderr tail. Caller MUST hold t.mu. The
// ringBuffer has its own leaf lock so calling String under t.mu is safe.
func (t *AppServer) stderrLocked() string {
	if t.stderr == nil {
		return ""
	}
	return t.stderr.String()
}

// Health returns a point-in-time snapshot of subprocess/transport health. The
// transport owns this truth (liveness, readiness, last error); callers read it
// for health endpoints rather than tracking subprocess state separately.
func (t *AppServer) Health() types.TransportHealth {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := types.TransportHealth{
		Connected: t.cmd != nil && t.ready,
		Ready:     t.ready,
		LastError: t.lastErr,
	}
	if t.cmd != nil && t.cmd.Process != nil {
		h.PID = t.cmd.Process.Pid
	}
	return h
}

// GetError returns the first transport-level error recorded (parse give-up or
// unexpected subprocess exit), or nil when healthy/clean.
func (t *AppServer) GetError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastErr
}

// Close shuts down the subprocess:
//  1. Close the demux (unblocks in-flight Send calls).
//  2. Close stdin to signal EOF; most agents exit cleanly on EOF.
//  3. Wait up to ShutdownGrace for Wait() to return.
//  4. SIGTERM; wait up to TerminateGrace.
//  5. SIGKILL as last resort.
//
// The ctx parameter is honored — if it's canceled, step 3/4 waits are cut
// short and we escalate faster.
func (t *AppServer) Close(ctx context.Context) error {
	var closeErr error
	t.closedOnce.Do(func() { closeErr = t.doClose(ctx) })
	return closeErr
}

func (t *AppServer) doClose(ctx context.Context) error {
	t.mu.Lock()
	// Mark shutdown requested BEFORE signaling the subprocess so the watchExit
	// goroutine classifies the resulting exit as requested (clean), not as an
	// unexpected death.
	t.shutdownReq = true
	demux := t.demux
	stdin := t.stdinW
	cmd := t.cmd
	waitDone := t.waitDone
	stderrDone := t.stderrDone
	t.mu.Unlock()

	if demux != nil {
		_ = demux.Close()
	}
	if stdin != nil {
		_ = stdin.Close()
	}

	if cmd == nil {
		return nil
	}

	// Stage 1: graceful exit within ShutdownGrace.
	exited, err := waitWithTimeout(ctx, waitDone, ShutdownGrace)
	if exited {
		t.drainStderr(stderrDone)
		return t.classifyExit(err, true)
	}

	// Stage 2: SIGTERM.
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	exited, err = waitWithTimeout(ctx, waitDone, TerminateGrace)
	if exited {
		t.drainStderr(stderrDone)
		return t.classifyExit(err, true)
	}

	// Stage 3: SIGKILL.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	err = <-waitDone
	t.drainStderr(stderrDone)
	return t.classifyExit(err, true)
}

// drainStderr waits for the stderr goroutine to finish so t.stderr is stable
// when callers read it. Call only after cmd.Wait has observed process exit or
// after the process has been killed; at that point the stderr pipe must reach
// EOF.
func (t *AppServer) drainStderr(done chan struct{}) {
	if done == nil {
		return
	}
	<-done
}

// classifyExit maps an *exec.Cmd Wait() error into either nil or a
// *types.ProcessError carrying exit code + stderr tail. Once Close has
// requested shutdown by closing stdin, a non-zero app-server exit is not
// actionable; turn/runtime failures are surfaced through RPC events.
func (t *AppServer) classifyExit(err error, shutdownRequested bool) error {
	if err == nil {
		return nil
	}
	if shutdownRequested {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return types.NewProcessError(
			"codex app-server exited non-zero",
			exitErr.ExitCode(),
			t.Stderr(),
		)
	}
	// Signal or other unexpected termination.
	return types.NewProcessError(
		"codex app-server terminated unexpectedly: "+err.Error(),
		-1,
		t.Stderr(),
	)
}

// waitWithTimeout returns (true, err) when waitDone fires, (false, nil) on
// timeout or context cancel.
func waitWithTimeout(ctx context.Context, waitDone chan error, timeout time.Duration) (bool, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waitDone:
		return true, err
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, nil
	}
}

// buildEnv overlays keyEqVals on os.Environ. An entry "KEY=" unsets KEY.
func buildEnv(keyEqVals []string) []string {
	if len(keyEqVals) == 0 {
		return os.Environ()
	}
	out := make([]string, 0, len(os.Environ())+len(keyEqVals))
	// Index existing entries by key for fast override.
	overrides := make(map[string]string, len(keyEqVals))
	for _, kv := range keyEqVals {
		k, v, _ := splitKV(kv)
		overrides[k] = v
	}
	for _, kv := range os.Environ() {
		k, _, _ := splitKV(kv)
		if _, overridden := overrides[k]; overridden {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		if v == "" {
			continue // unset
		}
		out = append(out, k+"="+v)
	}
	return out
}

// splitKV splits "KEY=VALUE" at the FIRST '=' — values may contain '='.
func splitKV(kv string) (key, value string, ok bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

// ringBuffer is a bounded byte buffer that keeps the most recent N bytes.
// Safe for concurrent Write from one goroutine and String from another.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	size int
	full bool
	pos  int // next write index when not full; write head when full
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{data: make([]byte, 0, size), size: size}
}

// Write implements io.Writer. Always returns (len(p), nil).
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	written := len(p)

	if !r.full {
		room := r.size - len(r.data)
		if len(p) <= room {
			r.data = append(r.data, p...)
			if len(r.data) == r.size {
				r.full = true
				r.pos = 0
			}
			return written, nil
		}
		r.data = append(r.data, p[:room]...)
		p = p[room:]
		r.full = true
		r.pos = 0
		// Fall through to the ring-mode path.
	}

	// Full-mode: write each byte at r.pos and advance.
	if len(p) >= r.size {
		p = p[len(p)-r.size:]
		copy(r.data, p)
		r.pos = 0
		return written, nil
	}
	n := copy(r.data[r.pos:], p)
	if n < len(p) {
		copy(r.data, p[n:])
		r.pos = len(p) - n
	} else {
		r.pos += n
		if r.pos == r.size {
			r.pos = 0
		}
	}
	return written, nil
}

// String returns the buffered bytes in chronological order.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return string(r.data)
	}
	var out bytes.Buffer
	out.Grow(r.size)
	out.Write(r.data[r.pos:])
	out.Write(r.data[:r.pos])
	return out.String()
}

// Compile-time interface assertion.
var _ Transport = (*AppServer)(nil)
