package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/hookbridge"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session/approval"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// Dispatcher consumes server messages and closes done after route cleanup.
type Dispatcher func(ctx context.Context, demux *jsonrpc.Demux, done chan struct{})

// Session owns one connection attempt, its transport, and dispatcher lifetime.
// Its zero value supports Close and accessors before a client is initialized.
type Session struct {
	threadRegistry *threadexec.Registry
	sink           EventSink
	approvals      approval.Owner

	opts   *types.CodexOptions
	logger *sdklog.Logger

	lifecycleMu sync.Mutex
	tr          *transport.AppServer
	runtime     *runtimeprobe.Connected
	initResult  InitializeResult

	// The demux stays published after Close so already-admitted callers can
	// observe its terminal error. Loading it does not acquire lifecycleMu.
	demux atomic.Pointer[jsonrpc.Demux]

	connectStarted bool
	connectCancel  context.CancelFunc
	connectToken   *struct{}

	onDispatcherClose func(context.CancelFunc) func() bool
	dispatcherCancel  context.CancelFunc
	dispatcherDone    chan struct{}

	hooks hookbridge.Installation

	// Readiness transitions share lifecycleMu with subscription enrollment.
	connected        atomic.Bool
	closed           atomic.Bool
	cliCompatibility atomic.Pointer[cliCompatibilityState]
}

// New configures a fresh session before it is shared with any callers.
func New(opts *types.CodexOptions, logger *sdklog.Logger, sink EventSink) Session {
	return Session{opts: opts, logger: logger, threadRegistry: threadexec.NewRegistry(), sink: sink}
}

// Connect spawns the subprocess, sends the initialize RPC, waits for the
// response, and sends the initialized notification. Starts the dispatcher
// goroutine that routes inbound notifications.
//
// Calling Connect more than once returns an error — create a new Client
// for a new session.
func (s *Session) Connect(ctx context.Context) (err error) {
	connectCtx, connectCancel, connectToken, err := s.beginConnect(ctx)
	if err != nil {
		return err
	}
	defer s.clearConnectCancel(connectCancel, connectToken)

	// Emit connect telemetry once the full handshake completes. err is the named
	// return, so the closure observes the terminal outcome.
	connectStart := time.Now()
	defer func() { s.opts.ObserverOrNop().OnConnect(time.Since(connectStart), err) }()
	defer func() {
		if err != nil {
			s.hooks.Close()
		}
	}()

	tr, cli, connErr := s.connectTransport(connectCtx)
	if connErr != nil {
		return connErr
	}
	demux := tr.Demux()
	initializeOptions := *s.opts
	runtime := runtimeprobe.NewConnected(cli, tr, initializeOptions.ExperimentalAPI)
	if publishErr := s.publishConnectingTransport(tr, demux, runtime); publishErr != nil {
		_ = tr.Close(context.WithoutCancel(ctx))
		return publishErr
	}

	// Send initialize.
	params := buildInitializeParams(&initializeOptions)
	resp, err := demux.Send(connectCtx, "initialize", params)
	if err != nil {
		// Detach cancellation for best-effort transport teardown; ctx may
		// already be canceled. Inherit values only.
		stderrTail := s.closeTransportAfterConnectError(ctx, tr)
		s.logInitializeFailure(err, stderrTail)
		return fmt.Errorf("codex.Client.Connect: initialize: %w", types.NewCLIConnectionError("initialize", err))
	}
	if resp.Error != nil {
		stderrTail := s.closeTransportAfterConnectError(ctx, tr)
		s.logInitializeFailure(types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data), stderrTail)
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	var initResult InitializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		s.logger.Warn("codex.Client.Connect: initialize response shape unrecognized",
			zap.Error(err))
	}

	s.lifecycleMu.Lock()
	s.initResult = initResult
	s.lifecycleMu.Unlock()

	// Send initialized notification.
	if err := demux.NotifyContext(connectCtx, "initialized", nil); err != nil {
		s.closeTransportAfterConnectError(ctx, tr)
		return fmt.Errorf("codex.Client.Connect: initialized: %w", err)
	}

	dispatcherCtx, dispatcherCancel := context.WithCancel(context.WithoutCancel(ctx))
	if err := s.StartDispatcher(dispatcherCtx, dispatcherCancel, demux, s.Dispatch); err != nil {
		dispatcherCancel()
		s.closeTransportAfterConnectError(ctx, tr)
		return err
	}

	s.logger.Debug("codex client connected",
		zap.String("user_agent", initResult.UserAgent),
		zap.String("codex_home", initResult.CodexHome))
	return nil
}

func (s *Session) connectTransport(ctx context.Context) (*transport.AppServer, transport.CLI, error) {
	// Hook-bridge auto-wiring is end-to-end. By default, HookCallback uses an
	// isolated CODEX_HOME containing SDK-generated hooks.json. Opt-in user-home
	// mode preserves the original backup/restore behavior.
	cli, err := ResolveCLIOptions(s.opts.CLIPath, s.opts.DefaultCwd, s.opts.Env, s.opts.EnvMode)
	if err != nil {
		return nil, transport.CLI{}, fmt.Errorf("codex.Client.Connect: %w", transport.CLIResolutionError(err))
	}
	if s.opts.HookCallback != nil {
		additions, hookErr := s.hooks.Setup(ctx, hookbridge.InstallationConfig{
			Handler:            s.opts.HookCallback,
			Timeout:            s.opts.HookTimeout,
			Logger:             s.logger,
			Mode:               s.opts.HookConfigMode,
			ShimPath:           s.opts.ShimPath,
			RequestedCodexHome: cli.EnvironmentValue("CODEX_HOME"),
		})
		if hookErr != nil {
			return nil, transport.CLI{}, fmt.Errorf("codex.Client.Connect: hook bridge: %w", hookErr)
		}
		cli = cli.WithEnvironmentAdditions(additions)
	}
	version, versionErr := cli.ProbeVersion(ctx)
	versionProbe := transport.CLIVersionProbeResult{Version: version, Err: versionErr}
	knownVersion := versionErr == nil
	s.cliCompatibility.Store(&cliCompatibilityState{version: versionProbe.Version, known: knownVersion})

	maxParseErrors := uint(0)
	if s.opts.MaxConsecutiveParseErrors != nil {
		maxParseErrors = *s.opts.MaxConsecutiveParseErrors
	}
	globalArgs, extraArgs := cliCompatibilityArgs(s.opts, versionProbe.Version, knownVersion)
	tr := transport.NewAppServer(transport.AppServerConfig{
		CLI:                       cli,
		GlobalArgs:                globalArgs,
		VersionProbe:              &versionProbe,
		ExtraArgs:                 extraArgs,
		Logger:                    s.logger,
		ReadBufferSize:            s.opts.ReadBufferSize,
		Observer:                  s.opts.ObserverOrNop(),
		MaxConsecutiveParseErrors: maxParseErrors,
	})
	if connectErr := tr.Connect(ctx); connectErr != nil {
		return nil, transport.CLI{}, fmt.Errorf("codex.Client.Connect: transport: %w", connectErr)
	}
	return tr, cli, nil
}

func (s *Session) beginConnect(ctx context.Context) (
	context.Context,
	context.CancelFunc,
	*struct{},
	error,
) {
	connectCtx, connectCancel := context.WithCancel(ctx)
	connectToken := &struct{}{}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closed.Load() {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	if s.connected.Load() {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientAlreadyConnected)
	}
	if s.connectStarted {
		connectCancel()
		return nil, nil, nil, fmt.Errorf("codex.Client.Connect: %w", types.ErrClientAlreadyConnected)
	}
	s.connectStarted = true
	s.connectCancel = connectCancel
	s.connectToken = connectToken
	return connectCtx, connectCancel, connectToken, nil
}

func (s *Session) clearConnectCancel(cancel context.CancelFunc, token *struct{}) {
	if cancel == nil {
		return
	}
	cancel()
	s.lifecycleMu.Lock()
	if s.connectToken == token {
		s.connectCancel = nil
		s.connectToken = nil
	}
	s.lifecycleMu.Unlock()
}

func (s *Session) publishConnectingTransport(tr *transport.AppServer, demux *jsonrpc.Demux, runtime *runtimeprobe.Connected) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	s.tr = tr
	s.demux.Store(demux)
	s.runtime = runtime
	return nil
}

// StartDispatcher publishes a handshake-complete connection and starts its
// dispatcher. A mock peer can supply its actual demux at this same boundary.
func (s *Session) StartDispatcher(
	dispatcherCtx context.Context,
	dispatcherCancel context.CancelFunc,
	demux *jsonrpc.Demux,
	dispatch Dispatcher,
) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: %w", types.ErrClientClosed)
	}
	published := s.demux.Load()
	if s.dispatcherDone != nil || (published != nil && published != demux) {
		return fmt.Errorf("codex.Client.Connect: %w", types.ErrClientAlreadyConnected)
	}
	if published == nil {
		s.demux.Store(demux)
	}
	s.connectStarted = true
	s.onDispatcherClose = func(cancel context.CancelFunc) func() bool {
		return context.AfterFunc(dispatcherCtx, cancel)
	}
	s.dispatcherCancel = dispatcherCancel
	s.dispatcherDone = make(chan struct{})
	s.connected.Store(true)
	go dispatch(dispatcherCtx, demux, s.dispatcherDone)
	return nil
}

// Close stops the dispatcher and then closes connection routes, transport and
// hooks. Route teardown runs outside the lifecycle lock.
func (s *Session) Close(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if ctx == nil {
		closed := s.closed.Load()
		s.lifecycleMu.Unlock()
		if closed {
			return nil
		}
		return fmt.Errorf("codex.Client.Close: context is required")
	}
	if s.closed.Load() {
		s.lifecycleMu.Unlock()
		return nil
	}
	s.closed.Store(true)
	connectCancel, dispatcherCancel, dispatcherDone := s.connectCancel, s.dispatcherCancel, s.dispatcherDone
	demux, tr := s.demux.Load(), s.tr
	s.lifecycleMu.Unlock()

	waitErr := stopClientDispatcher(ctx, connectCancel, dispatcherCancel, dispatcherDone, demux)
	if s.sink != nil {
		s.sink.Close()
	}
	s.threadRegistry.Close()

	var trErr error
	if tr != nil {
		trErr = tr.Close(ctx)
	}
	s.lifecycleMu.Lock()
	s.connected.Store(false)
	s.connectStarted = false
	s.connectCancel = nil
	s.connectToken = nil
	s.dispatcherCancel = nil
	s.onDispatcherClose = nil
	s.tr = nil
	s.runtime = nil
	s.lifecycleMu.Unlock()
	// The transport stops before the hook listener and configuration are
	// removed, so an in-flight hook subprocess can still reach its listener.
	s.hooks.Close()
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
