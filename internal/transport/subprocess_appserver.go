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
}

// AppServer is a Transport implementation that spawns `codex app-server`.
type AppServer struct {
	cfg    AppServerConfig
	logger *sdklog.Logger

	mu          sync.Mutex
	cmd         *exec.Cmd
	stdinW      io.WriteCloser
	demux       *jsonrpc.Demux
	stderr      *ringBuffer
	stderrDone  chan struct{}
	waitDone    chan error
	closedOnce  sync.Once
	connectOnce sync.Once
}

// NewAppServer constructs an AppServer transport. It does not spawn the
// subprocess — call Connect.
func NewAppServer(cfg AppServerConfig) *AppServer {
	logger := cfg.Logger
	if logger == nil {
		logger = sdklog.NewLoggerFromZap(nil)
	}
	return &AppServer{cfg: cfg, logger: logger}
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
	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Env = buildEnv(t.cfg.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return types.NewCLIConnectionError("stdin pipe", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return types.NewCLIConnectionError("stdout pipe", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return types.NewCLIConnectionError("stderr pipe", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return types.NewCLIConnectionError(fmt.Sprintf("spawn %q", cliPath), err)
	}

	t.cmd = cmd
	t.stdinW = stdin
	t.stderr = newRingBuffer(StderrRingSize)
	t.stderrDone = make(chan struct{})
	t.waitDone = make(chan error, 1)

	// Stderr drain goroutine — copies into ring buffer for diagnostics.
	go func() {
		defer close(t.stderrDone)
		_, _ = io.Copy(t.stderr, stderr)
	}()

	// Wait goroutine — observes exit for Close().
	go func() {
		t.waitDone <- cmd.Wait()
	}()

	lw := jsonrpc.NewLineWriter(stdin)
	bufSize := t.cfg.ReadBufferSize
	if bufSize < jsonrpc.MinReadBufferSize {
		bufSize = jsonrpc.MinReadBufferSize
	}
	lr := jsonrpc.NewLineReaderWithSize(stdout, bufSize)

	t.demux = jsonrpc.NewDemux(lr, lw, t.logger)
	t.demux.Run(ctx)

	t.logger.Debug("codex app-server spawned",
		zap.String("cli", cliPath),
		zap.Int("pid", cmd.Process.Pid))
	return nil
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
	if t.stderr == nil {
		return ""
	}
	return t.stderr.String()
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
		return t.classifyExit(err)
	}

	// Stage 2: SIGTERM.
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	exited, err = waitWithTimeout(ctx, waitDone, TerminateGrace)
	if exited {
		t.drainStderr(stderrDone)
		return t.classifyExit(err)
	}

	// Stage 3: SIGKILL.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	err = <-waitDone
	t.drainStderr(stderrDone)
	return t.classifyExit(err)
}

// drainStderr waits briefly for the stderr goroutine to finish so t.stderr
// is stable when callers read it. Bounded: we never block forever here.
func (t *AppServer) drainStderr(done chan struct{}) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
}

// classifyExit maps an *exec.Cmd Wait() error into either nil (clean exit)
// or a *types.ProcessError carrying exit code + stderr tail.
func (t *AppServer) classifyExit(err error) error {
	if err == nil {
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
