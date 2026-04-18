package hookbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// DefaultCallbackTimeout bounds how long the listener waits for the
// user's HookHandler to return before killing the hook with a default
// "allow" response. 30s matches the shim's default hook timeout.
const DefaultCallbackTimeout = 30 * time.Second

// MaxFrameSize caps incoming request frames at 16 MiB.
const MaxFrameSize = 16 * 1024 * 1024

// Listener owns the Unix socket that the shim dials. One Listener per
// Client. Concurrent hook fires are served on separate goroutines.
type Listener struct {
	socketPath string
	ln         net.Listener
	handler    types.HookHandler
	timeout    time.Duration
	logger     *sdklog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	wg     sync.WaitGroup
	closed bool
	mu     sync.Mutex
}

// Config configures a Listener.
type Config struct {
	SocketPath string
	Handler    types.HookHandler
	Timeout    time.Duration
	Logger     *sdklog.Logger
}

// New creates and starts a Listener. The socket file is created at
// SocketPath; the caller is responsible for choosing a unique path (the
// SDK uses a PID-tagged tempdir). Returns after the accept loop is
// running.
//
// Caller MUST invoke Close to release the socket file.
func New(cfg Config) (*Listener, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("hookbridge.New: SocketPath required")
	}
	if cfg.Handler == nil {
		return nil, fmt.Errorf("hookbridge.New: Handler required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultCallbackTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = sdklog.NewLoggerFromZap(nil)
	}

	// Remove a stale socket file at this path (could exist from a crashed
	// prior SDK process). If another live listener owns it, the bind
	// below fails — propagated as an error.
	_ = os.Remove(cfg.SocketPath)

	ln, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("hookbridge.New: listen %q: %w", cfg.SocketPath, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{
		socketPath: cfg.SocketPath,
		ln:         ln,
		handler:    cfg.Handler,
		timeout:    cfg.Timeout,
		logger:     cfg.Logger,
		ctx:        ctx,
		cancel:     cancel,
	}
	l.wg.Add(1)
	go l.acceptLoop()
	return l, nil
}

// SocketPath returns the Unix socket path the shim should dial.
func (l *Listener) SocketPath() string { return l.socketPath }

// Close stops accepting new connections, waits for in-flight hook
// callbacks to finish (bounded by their own timeouts), and removes the
// socket file. Idempotent.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()

	l.cancel()
	_ = l.ln.Close()
	l.wg.Wait()
	_ = os.Remove(l.socketPath)
	return nil
}

func (l *Listener) acceptLoop() {
	defer l.wg.Done()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if l.ctx.Err() != nil {
				return // closed
			}
			// transient accept error — brief backoff
			l.logger.Warn("hookbridge accept", zap.Error(err))
			select {
			case <-time.After(25 * time.Millisecond):
			case <-l.ctx.Done():
				return
			}
			continue
		}
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			l.serve(conn)
		}()
	}
}

func (l *Listener) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(l.timeout))

	reqBytes, err := readFrame(conn)
	if err != nil {
		l.logger.Warn("hookbridge read frame", zap.Error(err))
		return
	}
	var req HookRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		l.logger.Warn("hookbridge unmarshal request", zap.Error(err))
		return
	}

	// Parse the hook input JSON from req.Stdin.
	var in types.HookInput
	if err := json.Unmarshal([]byte(req.Stdin), &in); err != nil {
		l.logger.Warn("hookbridge parse hook stdin", zap.Error(err),
			zap.String("shim_version", req.ShimVersion))
		return
	}
	in.Raw = []byte(req.Stdin)

	// Invoke user callback under a context bounded by the configured
	// timeout. If the callback panics, recover and default to allow.
	cbCtx, cbCancel := context.WithTimeout(l.ctx, l.timeout)
	defer cbCancel()

	decision := runHandlerWithRecover(cbCtx, l.handler, in, l.logger)
	resp := DecisionToResponse(in.HookEventName, decision)

	respBytes, err := json.Marshal(resp)
	if err != nil {
		l.logger.Warn("hookbridge marshal response", zap.Error(err))
		return
	}
	_ = conn.SetDeadline(time.Now().Add(l.timeout))
	if err := writeFrame(conn, respBytes); err != nil {
		l.logger.Warn("hookbridge write response", zap.Error(err))
	}
}

// runHandlerWithRecover invokes handler on a goroutine bounded by ctx.
// If the handler panics, logs and returns HookAllow (fail-open so the
// user's codex run never bricks).
func runHandlerWithRecover(ctx context.Context, handler types.HookHandler, in types.HookInput, logger *sdklog.Logger) types.HookDecision {
	done := make(chan types.HookDecision, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warn("hookbridge: hook callback panicked",
					zap.Any("panic", r),
					zap.String("event", string(in.HookEventName)))
				done <- types.HookAllow{}
			}
		}()
		done <- handler(ctx, in)
	}()
	select {
	case d := <-done:
		return d
	case <-ctx.Done():
		logger.Warn("hookbridge: hook callback timed out",
			zap.String("event", string(in.HookEventName)),
			zap.Error(ctx.Err()))
		return types.HookAllow{} // fail open
	}
}

func writeFrame(w io.Writer, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > MaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d > %d", n, MaxFrameSize)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf, nil
}
