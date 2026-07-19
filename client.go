package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/hookbridge"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// Client is the top-level entry point to the Codex SDK. A Client owns a
// single `codex app-server` subprocess and exposes thread lifecycle + turn
// execution via the Thread type.
//
// Create with NewClient, then call Connect once before any thread
// operations. Close on shutdown — the Client never reconnects.
type Client struct {
	opts   *types.CodexOptions
	logger *sdklog.Logger
	tr     *transport.AppServer
	demux  *jsonrpc.Demux

	// InitializeResult from the handshake. Populated during Connect.
	initResult InitializeResult

	mu               sync.Mutex
	threads          map[string]*Thread
	latestThreadID   string
	cliCompatibility atomic.Pointer[cliCompatibilityState]

	lifecycleMu    sync.Mutex
	connectStarted atomic.Bool
	connectCancel  context.CancelFunc
	connectToken   *struct{}

	// Dispatcher lifecycle.
	dispatcherCtx    context.Context
	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

	// Hook bridge — populated only when HookCallback is registered.
	hookBridgeMu sync.Mutex
	hookListener *hookbridge.Listener
	// hookHooksJSONPath is the absolute path to the user's
	// ~/.codex/hooks.json that the SDK overwrote during Connect.
	hookHooksJSONPath string
	// hookBackupPath, when non-empty, is the absolute path to the byte-for-byte
	// backup of the user's pre-Connect hooks.json. Empty when no hooks.json
	// existed before Connect (Close removes the SDK-written hooks.json instead).
	hookBackupPath string
	// hookCodexHome is non-empty only for isolated hook mode; Close removes it.
	hookCodexHome string
	// hookSocketDir is non-empty only when the hook socket was relocated under a
	// freshly-created 0700 directory (the sun_path-overflow fallback); Close
	// removes it. The 0700 parent is the authoritative owner-only access gate.
	hookSocketDir string
	// hookHadUserConfig records whether a hooks.json existed at Connect time.
	hookHadUserConfig bool

	connected atomic.Bool
	closed    atomic.Bool
}

// InitializeResult is the response payload from the `initialize` RPC,
// exposed for callers that want to inspect the server's environment.
type InitializeResult struct {
	UserAgent      string `json:"userAgent,omitempty"`
	CodexHome      string `json:"codexHome,omitempty"`
	PlatformFamily string `json:"platformFamily,omitempty"`
	PlatformOs     string `json:"platformOs,omitempty"`
}

// NewClient constructs a Client. Options must be non-nil. The returned
// Client is NOT yet connected — call Connect before any thread calls.
func NewClient(ctx context.Context, opts *types.CodexOptions) (*Client, error) {
	if opts == nil {
		return nil, fmt.Errorf("codex.NewClient: opts must not be nil")
	}
	logger := sdklog.NewLoggerFromZap(opts.Logger)
	if opts.Logger == nil && opts.Verbose {
		logger = sdklog.NewLogger(true)
	}
	return &Client{
		opts:    opts,
		logger:  logger,
		threads: make(map[string]*Thread),
	}, nil
}

// Connect spawns the subprocess, sends the initialize RPC, waits for the
// response, and sends the initialized notification. Starts the dispatcher
// goroutine that routes inbound notifications to Thread inboxes.
//
// Calling Connect more than once returns an error — create a new Client
// for a new session.
func (c *Client) Connect(ctx context.Context) (err error) {
	connectCtx, connectCancel, connectToken, err := c.beginConnect(ctx)
	if err != nil {
		return err
	}
	defer c.clearConnectCancel(connectCancel, connectToken)

	// Emit connect telemetry once the full handshake completes. err is the named
	// return, so the closure observes the terminal outcome.
	connectStart := time.Now()
	defer func() { c.opts.ObserverOrNop().OnConnect(time.Since(connectStart), err) }()
	defer func() {
		if err != nil {
			c.closeHookBridge()
		}
	}()

	tr, connErr := c.connectTransport(connectCtx)
	if connErr != nil {
		return connErr
	}
	demux := tr.Demux()
	if publishErr := c.publishConnectingTransport(tr, demux); publishErr != nil {
		_ = tr.Close(context.WithoutCancel(ctx))
		return publishErr
	}

	// Send initialize.
	params := buildInitializeParams(c.opts)
	resp, err := demux.Send(connectCtx, "initialize", params)
	if err != nil {
		// Detach cancellation for best-effort transport teardown; ctx may
		// already be canceled. Inherit values only.
		stderrTail := c.closeTransportAfterConnectError(ctx, tr)
		c.logInitializeFailure(err, stderrTail)
		return fmt.Errorf("codex.Client.Connect: initialize: %w", types.NewCLIConnectionError("initialize", err))
	}
	if resp.Error != nil {
		stderrTail := c.closeTransportAfterConnectError(ctx, tr)
		c.logInitializeFailure(types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data), stderrTail)
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	if err := json.Unmarshal(resp.Result, &c.initResult); err != nil {
		c.logger.Warn("codex.Client.Connect: initialize response shape unrecognized",
			zap.Error(err))
	}

	// Send initialized notification.
	if err := demux.Notify("initialized", nil); err != nil {
		c.closeTransportAfterConnectError(ctx, tr)
		return fmt.Errorf("codex.Client.Connect: initialized: %w", err)
	}

	dispatcherCtx, dispatcherCancel := context.WithCancel(context.WithoutCancel(ctx))
	if err := c.startDispatcher(dispatcherCtx, dispatcherCancel, demux); err != nil {
		dispatcherCancel()
		c.closeTransportAfterConnectError(ctx, tr)
		return err
	}

	c.logger.Debug("codex client connected",
		zap.String("user_agent", c.initResult.UserAgent),
		zap.String("codex_home", c.initResult.CodexHome))
	return nil
}

func (c *Client) connectTransport(ctx context.Context) (*transport.AppServer, error) {
	// Hook-bridge auto-wiring is end-to-end. By default, HookCallback uses an
	// isolated CODEX_HOME containing SDK-generated hooks.json. Opt-in user-home
	// mode preserves the original backup/restore behavior.
	extraEnv := append([]string(nil), c.opts.Env...)
	if c.opts.HookCallback != nil {
		if err := c.setupHookBridge(ctx, &extraEnv); err != nil {
			return nil, fmt.Errorf("codex.Client.Connect: hook bridge: %w", err)
		}
	}
	versionProbe := probeCLICompatibilityVersion(ctx, c.opts, extraEnv)
	knownVersion := versionProbe.Err == nil
	c.cliCompatibility.Store(&cliCompatibilityState{version: versionProbe.Version, known: knownVersion})

	maxParseErrors := uint(0)
	if c.opts.MaxConsecutiveParseErrors != nil {
		maxParseErrors = *c.opts.MaxConsecutiveParseErrors
	}
	globalArgs, extraArgs := cliCompatibilityArgs(c.opts, versionProbe.Version, knownVersion)
	tr := transport.NewAppServer(transport.AppServerConfig{
		CLIPath:                   c.opts.CLIPath,
		Cwd:                       c.opts.DefaultCwd,
		GlobalArgs:                globalArgs,
		VersionProbe:              &versionProbe,
		ExtraArgs:                 extraArgs,
		Env:                       extraEnv,
		Logger:                    c.logger,
		ReadBufferSize:            c.opts.ReadBufferSize,
		Observer:                  c.opts.ObserverOrNop(),
		MaxConsecutiveParseErrors: maxParseErrors,
	})
	if err := tr.Connect(ctx); err != nil {
		return nil, fmt.Errorf("codex.Client.Connect: transport: %w", err)
	}
	return tr, nil
}

func (c *Client) beginConnect(ctx context.Context) (
	context.Context,
	context.CancelFunc,
	*struct{},
	error,
) {
	connectCtx, connectCancel := context.WithCancel(ctx)
	connectToken := &struct{}{}
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	if c.closed.Load() {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	if c.connected.Load() {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientAlreadyConnected)
	}
	if !c.connectStarted.CompareAndSwap(false, true) {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientAlreadyConnected)
	}
	c.connectCancel = connectCancel
	c.connectToken = connectToken
	return connectCtx, connectCancel, connectToken, nil
}

func (c *Client) clearConnectCancel(cancel context.CancelFunc, token *struct{}) {
	if cancel == nil {
		return
	}
	cancel()
	c.lifecycleMu.Lock()
	if c.connectToken == token {
		c.connectCancel = nil
		c.connectToken = nil
	}
	c.lifecycleMu.Unlock()
}

func (c *Client) publishConnectingTransport(tr *transport.AppServer, demux *jsonrpc.Demux) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	c.tr = tr
	c.demux = demux
	return nil
}

func (c *Client) startDispatcher(
	dispatcherCtx context.Context,
	dispatcherCancel context.CancelFunc,
	demux *jsonrpc.Demux,
) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	c.dispatcherCtx = dispatcherCtx
	c.dispatcherCancel = dispatcherCancel
	c.dispatcherDone = make(chan struct{})
	go c.dispatch(dispatcherCtx, demux, c.dispatcherDone)
	c.connected.Store(true)
	return nil
}

func (c *Client) closeTransportAfterConnectError(ctx context.Context, tr *transport.AppServer) string {
	if c.closed.Load() {
		return redactedStderrDiagnosticTail(tr.Stderr())
	}
	_ = tr.Close(context.WithoutCancel(ctx))
	return redactedStderrDiagnosticTail(tr.Stderr())
}

func (c *Client) logInitializeFailure(err error, stderrTail string) {
	fields := []zap.Field{zap.String("error_kind", initializeFailureKind(err))}
	var rpcErr *types.RPCError
	if errors.As(err, &rpcErr) {
		fields = append(fields, zap.Int("rpc_error_code", rpcErr.Code))
	}
	if stderrTail != "" {
		fields = append(fields, zap.String("stderr_tail", stderrTail))
	}
	c.logger.Warn("codex.Client.Connect: initialize failed", fields...)
}

const stderrDiagnosticTailLimit = 2048

const (
	redactedStderrLine                    = "<redacted>"
	controlProtocolInitializationFailed   = "control protocol initialization failed"
	initializeFailureKindDeadlineExceeded = "deadline_exceeded"
	initializeFailureKindContextCanceled  = "context_canceled"
	initializeFailureKindRPCError         = "rpc_error"
	initializeFailureKindOther            = "other"
)

func initializeFailureKind(err error) string {
	var rpcErr *types.RPCError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return initializeFailureKindDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return initializeFailureKindContextCanceled
	case errors.As(err, &rpcErr):
		return initializeFailureKindRPCError
	default:
		return initializeFailureKindOther
	}
}

func redactedStderrDiagnosticTail(stderr string) string {
	if stderr == "" {
		return ""
	}
	stderr = redactStderrDiagnosticLines(stderr)
	truncated := false
	if len(stderr) > stderrDiagnosticTailLimit {
		stderr = stderr[len(stderr)-stderrDiagnosticTailLimit:]
		truncated = true
	}
	if truncated {
		return "[truncated]\n" + stderr
	}
	return stderr
}

func redactStderrDiagnosticLines(stderr string) string {
	lines := strings.SplitAfter(stderr, "\n")
	for i, line := range lines {
		lines[i] = redactStderrDiagnosticLine(line)
	}
	return strings.Join(lines, "")
}

func redactStderrDiagnosticLine(line string) string {
	if line == "" {
		return ""
	}
	suffix := ""
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
		suffix = "\n"
	}
	if strings.TrimSpace(line) == "" {
		return suffix
	}
	if strings.HasPrefix(strings.TrimSpace(line), controlProtocolInitializationFailed) {
		return controlProtocolInitializationFailed + suffix
	}
	return redactedStderrLine + suffix
}

func buildInitializeParams(opts *types.CodexOptions) map[string]any {
	capabilities := map[string]any{
		"experimentalApi": opts.ExperimentalAPI,
	}
	if len(opts.OptOutNotificationMethods) > 0 {
		capabilities["optOutNotificationMethods"] = append([]string(nil), opts.OptOutNotificationMethods...)
	}
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    opts.ClientName,
			"version": opts.ClientVersion,
			"title":   opts.ClientTitle,
		},
		"capabilities": capabilities,
	}
}

// InitializeResult returns the response payload received during Connect.
// Only meaningful after Connect returns nil.
func (c *Client) InitializeResult() InitializeResult { return c.initResult }

// ProcessID returns the OS pid of the codex app-server subprocess, or 0
// when Connect has not yet succeeded or the subprocess has exited.
// Higher layers use this for lifecycle management — e.g., killing the
// subprocess directly on worktree switch so Close's graceful-exit ladder
// doesn't have to wait.
func (c *Client) ProcessID() int {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.tr == nil {
		return 0
	}
	return c.tr.Pid()
}

// Health returns a snapshot of the underlying transport/subprocess health
// (connected, ready, PID, last error). It returns a zero TransportHealth when
// the transport does not expose health (e.g. before Connect) so callers can read
// it for a health endpoint rather than reconstructing liveness themselves.
func (c *Client) Health() types.TransportHealth {
	// Inline-interface type assertion mirrors the claude SDK: the transport
	// concrete type owns Health(), and the Client surfaces it without widening
	// the narrow Transport interface for non-health callers. The nil check comes
	// first — a typed-nil *AppServer still satisfies the interface, and its
	// Health() would dereference a nil receiver's mutex.
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.tr == nil {
		return types.TransportHealth{}
	}
	type healthProvider interface {
		Health() types.TransportHealth
	}
	if hp, ok := any(c.tr).(healthProvider); ok {
		return hp.Health()
	}
	return types.TransportHealth{}
}

// SessionID returns the ID of the most recently started or resumed
// thread, or "" when no thread has been registered yet (or the latest
// was unregistered). The Codex SDK supports multiple concurrent threads
// per client; callers that need a specific thread's ID should use
// Thread.ID() on the handle they obtained from StartThread/ResumeThread.
//
// This accessor exists for single-thread callers that want a stable
// correlation ID without tracking the Thread handle themselves — e.g.,
// a daemon session that maps 1:1 onto a codex thread and needs to
// persist the thread ID for crash-recovery resume.
func (c *Client) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestThreadID
}

// Close shuts down the dispatcher, closes every Thread's inbox, and
// terminates the subprocess with the 3-stage graceful-shutdown ladder.
// Safe to call multiple times.
func (c *Client) Close(ctx context.Context) error {
	if ctx == nil {
		if c.closed.Load() {
			return nil
		}
		return fmt.Errorf("codex.Client.Close: context is required")
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	connectCancel, dispatcherCancel, dispatcherDone, demux, tr := c.closeSnapshot()
	waitErr := stopClientDispatcher(ctx, connectCancel, dispatcherCancel, dispatcherDone, demux)

	c.mu.Lock()
	for _, t := range c.threads {
		t.markClosed()
	}
	c.threads = nil
	c.latestThreadID = ""
	c.mu.Unlock()

	var trErr error
	if tr != nil {
		trErr = tr.Close(ctx)
	}
	c.lifecycleMu.Lock()
	c.connected.Store(false)
	c.connectStarted.Store(false)
	c.connectCancel = nil
	c.connectToken = nil
	c.dispatcherCancel = nil
	c.tr = nil
	c.lifecycleMu.Unlock()
	// Tear down the hook bridge AFTER the transport is stopped so no
	// in-flight hook subprocess can still dial the socket.
	c.closeHookBridge()
	return errors.Join(trErr, waitErr)
}

func stopClientDispatcher(
	ctx context.Context,
	connectCancel context.CancelFunc,
	dispatcherCancel context.CancelFunc,
	dispatcherDone chan struct{},
	demux *jsonrpc.Demux,
) error {
	if connectCancel != nil {
		connectCancel()
	}
	if dispatcherCancel != nil {
		dispatcherCancel()
	}
	if demux != nil {
		_ = demux.Close()
	}
	if dispatcherDone == nil {
		return nil
	}
	select {
	case <-dispatcherDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("codex.Client.Close: waiting for dispatcher: %w", ctx.Err())
	}
}

func (c *Client) closeSnapshot() (
	connectCancel context.CancelFunc,
	dispatcherCancel context.CancelFunc,
	dispatcherDone chan struct{},
	demux *jsonrpc.Demux,
	tr *transport.AppServer,
) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.connectCancel, c.dispatcherCancel, c.dispatcherDone, c.demux, c.tr
}
