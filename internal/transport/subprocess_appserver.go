package transport

import (
	"context"
	"io"
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

// StderrDrainTimeout bounds how long the transport waits for the stderr drain
// goroutine to reach EOF. The drain only finishes once every writer of the
// subprocess's stderr pipe closes it; on the SIGKILL path a descendant that
// inherited the write-end can keep it open after the parent is reaped, so the
// wait MUST be bounded or Close (and watchExit) would hang forever. Matches the
// claude-agent-sdk-go sibling's bound.
const StderrDrainTimeout = 500 * time.Millisecond

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
	t.logCLIVersion(ctx, cliPath)

	args := append([]string{"app-server"}, t.cfg.ExtraArgs...)

	proc, err := t.spawnWithRetry(ctx, cliPath, args)
	if err != nil {
		return err
	}

	stderr := newRingBuffer(StderrRingSize)
	stderrDone := make(chan struct{})
	waitDone := make(chan error, 1)
	lw := jsonrpc.NewLineWriter(proc.stdin)
	bufSize := t.cfg.ReadBufferSize
	if bufSize < jsonrpc.MinReadBufferSize {
		bufSize = jsonrpc.MinReadBufferSize
	}
	lr := jsonrpc.NewLineReaderWithSize(proc.stdout, bufSize)
	demux := jsonrpc.NewDemux(lr, lw, t.logger,
		jsonrpc.WithObserver(t.observer),
		jsonrpc.WithMaxParseErrors(t.cfg.MaxConsecutiveParseErrors),
		jsonrpc.WithUnrecoverableHandler(t.terminateOnUnrecoverableError),
	)

	t.mu.Lock()
	t.cmd = proc.cmd
	t.stdinW = proc.stdin
	t.demux = demux
	t.stderr = stderr
	t.stderrDone = stderrDone
	t.waitDone = waitDone
	t.ready = true
	t.mu.Unlock()

	// Stderr drain goroutine — copies into ring buffer for diagnostics.
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderr, proc.stderr)
	}()

	// Wait goroutine — observes exit for Close() and emits the single
	// OnSubprocessExit telemetry. Exit info is captured under the lock; the
	// Observer is invoked AFTER unlock so it can never block under the mutex.
	go t.watchExit(proc.cmd, stderrDone)

	demux.Run(ctx)

	t.logger.Debug("codex app-server spawned",
		zap.String("cli", cliPath),
		zap.Int("pid", proc.cmd.Process.Pid))
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

// Compile-time interface assertion.
var _ Transport = (*AppServer)(nil)
