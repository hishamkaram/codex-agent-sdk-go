package codex

import (
	"context"
	"errors"
	"fmt"

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
