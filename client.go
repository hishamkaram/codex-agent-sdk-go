package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Client is the top-level entry point to the Codex SDK. A Client owns a
// single `codex app-server` subprocess and exposes thread lifecycle + turn
// execution via the Thread type.
//
// Create with NewClient, then call Connect once before any thread
// operations. Close on shutdown — the Client never reconnects.
type Client struct {
	opts          *types.CodexOptions
	logger        *sdklog.Logger
	session       session.Session
	subscriptions threadSubscriptions
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
	c := &Client{opts: opts, logger: logger}
	c.session = session.New(opts, logger, &c.subscriptions)
	return c, nil
}

// Connect spawns the subprocess, sends the initialize RPC, waits for the
// response, and sends the initialized notification. Starts the dispatcher
// goroutine that routes inbound notifications to Thread inboxes.
//
// Calling Connect more than once returns an error — create a new Client
// for a new session.
func (c *Client) Connect(ctx context.Context) (err error) { return c.session.Connect(ctx) }

// InitializeResult returns the response payload received during Connect.
// Only meaningful after Connect returns nil.
func (c *Client) InitializeResult() InitializeResult {
	return InitializeResult(c.session.InitializeResult())
}

// ProcessID returns the OS pid of the codex app-server subprocess, or 0
// when Connect has not yet succeeded or the subprocess has exited.
// Higher layers use this for lifecycle management — e.g., killing the
// subprocess directly on worktree switch so Close's graceful-exit ladder
// doesn't have to wait.
func (c *Client) ProcessID() int { return c.session.ProcessID() }

// Health returns a snapshot of the underlying transport/subprocess health
// (connected, ready, PID, last error). It returns a zero TransportHealth when
// the transport does not expose health (e.g. before Connect) so callers can read
// it for a health endpoint rather than reconstructing liveness themselves.
func (c *Client) Health() types.TransportHealth { return c.session.Health() }

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
func (c *Client) SessionID() string { return c.session.Threads().Latest() }

// Close shuts down the dispatcher, closes every Thread's inbox, and
// terminates the subprocess with the 3-stage graceful-shutdown ladder.
// Safe to call multiple times.
func (c *Client) Close(ctx context.Context) error {
	if ctx != nil {
		if err := c.checkApprovalReentry(ctx); err != nil {
			return err
		}
	}
	return c.session.Close(ctx)
}

// ErrApprovalCallbackReentry means an approval callback attempted to start
// another blocking operation or close its own Client. Return the decision
// before doing work that may depend on approval processing.
var ErrApprovalCallbackReentry = errors.New("codex: blocking client operation in approval callback")

func (c *Client) checkApprovalReentry(ctx context.Context) error {
	if c.session.IsApprovalReentry(ctx) {
		return ErrApprovalCallbackReentry
	}
	return nil
}

// Query is a fire-and-forget convenience wrapper that creates a Client,
// connects, starts a throwaway thread, runs one turn, and returns the
// streamed events channel. The Client and Thread are closed automatically
// after the channel is drained.
//
// Use this for simple one-off scripts. For multi-turn work, use NewClient
// + StartThread directly so you can reuse the subprocess.
func Query(ctx context.Context, prompt string, opts *types.CodexOptions) (<-chan types.ThreadEvent, error) {
	if opts == nil {
		opts = types.NewCodexOptions()
	}
	client, err := NewClient(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	thread, startErr := client.StartThread(ctx, nil)
	if startErr != nil {
		// Detach cancellation for best-effort cleanup: the parent ctx may
		// already be canceled (that can be why StartThread failed), but
		// Close must still tear down the subprocess. Inherit values only.
		_ = client.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("codex.Query: StartThread: %w", startErr)
	}
	events, runErr := thread.RunStreamed(ctx, prompt, nil)
	if runErr != nil {
		_ = client.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("codex.Query: RunStreamed: %w", runErr)
	}

	// Wrap the inner channel in a goroutine that forwards events and cleans
	// up the Client when the inner channel closes.
	out := make(chan types.ThreadEvent, ThreadInboxBuffer)
	go func() {
		defer close(out)
		defer func() { _ = client.Close(context.WithoutCancel(ctx)) }()
		for ev := range events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// StartupOption configures the warm-pool pre-spawn step.
type StartupOption func(*startupConfig)

type startupConfig struct {
	cliPath string
}

// WithStartupCLIPath overrides the codex binary location for the warm
// pool.
func WithStartupCLIPath(path string) StartupOption {
	return func(c *startupConfig) { c.cliPath = path }
}

// Startup pre-warms the SDK's internal subprocess pool so the first Query
// or NewClient+Connect avoids a ~200-400 ms cold-start cost.
//
// In v0.1.0 this is a NO-OP stub — the SDK always spawns on demand.
// The API is stable so users who adopt Startup today won't need to change
// call sites when the pool lands in v0.2.
func Startup(ctx context.Context, opts ...StartupOption) error {
	_ = ctx
	cfg := &startupConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	return nil
}

// sendRaw is the common skeleton for a Send + connection-state +
// RPC-error check. Returns the raw result bytes; caller decodes into
// the typed struct. Embeds the caller name in every error so log
// readers can identify the public method instantly.
//
// Wire-level RPCErrors are translated to typed SDK errors when the
// codex error message matches a known pattern (e.g., "requires
// experimentalApi capability" → *types.FeatureNotEnabledError).
// Otherwise the generic *types.RPCError is returned.
func (c *Client) sendRaw(ctx context.Context, callerName, method string, params any) (json.RawMessage, error) {
	if c.session.Closed() {
		return nil, fmt.Errorf("codex.Client.%s: %w", callerName, types.ErrClientClosed)
	}
	if !c.session.Connected() {
		return nil, fmt.Errorf("codex.Client.%s: %w", callerName, types.ErrClientNotConnected)
	}
	resp, err := c.session.Demux().Send(ctx, method, params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.%s: %w", callerName, err)
	}
	if resp.Error != nil {
		return nil, classifyRPCError(method, resp.Error)
	}
	return resp.Result, nil
}

// classifyRPCError maps a wire-level RPC error to a typed SDK error
// when the message matches a known pattern. Verified live against
// codex 0.121.0:
//
//	"thread/backgroundTerminals/clean requires experimentalApi capability"
//
// Future patterns can be added here without changing call sites.
func classifyRPCError(method string, e *jsonrpc.RPCError) error {
	if e == nil {
		return nil
	}
	legacyUnsupported := e.Code == -32600 && e.Message == "Invalid request"
	if method == "configRequirements/read" && (e.Code == -32601 || legacyUnsupported) {
		return fmt.Errorf("codex.Client.ReadConfigRequirements: %w", ErrRuntimeControlsUnsupported)
	}
	if strings.Contains(e.Message, "requires experimentalApi capability") {
		return types.NewFeatureNotEnabledError("experimentalApi", method, e.Message)
	}
	return types.NewRPCError(e.Code, e.Message, e.Data)
}
