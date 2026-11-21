package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// InitializeResult is the response payload from the initialize RPC.
type InitializeResult struct {
	UserAgent      string `json:"userAgent,omitempty"`
	CodexHome      string `json:"codexHome,omitempty"`
	PlatformFamily string `json:"platformFamily,omitempty"`
	PlatformOs     string `json:"platformOs,omitempty"`
}

// InitializeResult reads the handshake metadata. It is meaningful after Connect.
func (s *Session) InitializeResult() InitializeResult {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.initResult
}

// ProcessID returns the process ID, if the transport has started.
func (s *Session) ProcessID() int {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.tr == nil {
		return 0
	}
	return s.tr.Pid()
}

// Health reads the current transport snapshot.
func (s *Session) Health() types.TransportHealth {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.tr == nil {
		return types.TransportHealth{}
	}
	return s.tr.Health()
}

// WorkingDirectory returns the directory captured for this launch.
func (s *Session) WorkingDirectory() string {
	s.lifecycleMu.Lock()
	tr := s.tr
	s.lifecycleMu.Unlock()
	if tr == nil {
		return ""
	}
	return tr.WorkingDirectory()
}

// Demux returns the published RPC session, including after shutdown.
func (s *Session) Demux() *jsonrpc.Demux { return s.demux.Load() }

// DispatcherDone returns the signal closed by the active dispatcher on exit.
func (s *Session) DispatcherDone() <-chan struct{} {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.dispatcherDone
}

// Connected reports whether message dispatch is ready.
func (s *Session) Connected() bool { return s.connected.Load() }

// Closed reports whether explicit shutdown has begun.
func (s *Session) Closed() bool { return s.closed.Load() }

// WithReady admits a short, internal route installation before shutdown or
// source loss. install runs under the lifecycle lock and must not call Session.
func (s *Session) WithReady(install func()) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Load() {
		return types.ErrClientClosed
	}
	if !s.connected.Load() {
		return types.ErrClientNotConnected
	}
	install()
	return nil
}

// MarkSourceLost marks the dispatcher unavailable before it reports a gap.
// It returns false when explicit shutdown has already started.
func (s *Session) MarkSourceLost() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Load() {
		return false
	}
	s.connected.Store(false)
	return true
}

// MarkDisconnected clears readiness at normal dispatcher exit.
func (s *Session) MarkDisconnected() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.connected.Store(false)
}

// RequireSchema checks connection readiness and proves a feature for the
// lifetime of the connected executable.
func (s *Session) RequireSchema(ctx context.Context, method, name string, supported func(string) (bool, error), unsupported error, timeout time.Duration) error {
	s.lifecycleMu.Lock()
	runtime, onClose := s.runtime, s.onDispatcherClose
	connected := s.connected.Load()
	s.lifecycleMu.Unlock()
	if s.closed.Load() {
		return fmt.Errorf("%s: %w", method, types.ErrClientClosed)
	}
	if runtime == nil || !runtime.Enabled() {
		return types.NewFeatureNotEnabledError("experimentalApi", method, "set WithExperimentalAPI(true) before connecting")
	}
	if !connected {
		return fmt.Errorf("%s: %w", method, types.ErrClientNotConnected)
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := onClose(cancel)
	defer stop()
	probeErr := runtime.RequireSchema(probeCtx, method, name, supported, unsupported)
	if callerErr := ctx.Err(); callerErr != nil {
		return fmt.Errorf("%s: check experimental schema: %w", method, callerErr)
	}
	if s.closed.Load() {
		return fmt.Errorf("%s: check experimental schema: %w", method, types.ErrClientClosed)
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s: experimental schema probe exceeded the SDK deadline: %w", method, unsupported)
	}
	return probeErr
}
