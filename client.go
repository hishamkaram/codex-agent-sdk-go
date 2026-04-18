package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
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

	mu      sync.Mutex
	threads map[string]*Thread

	// Dispatcher lifecycle.
	dispatcherCtx    context.Context
	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

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
func (c *Client) Connect(ctx context.Context) error {
	if !c.connected.CompareAndSwap(false, true) {
		return fmt.Errorf("codex.Client.Connect: already connected")
	}
	if c.closed.Load() {
		return fmt.Errorf("codex.Client.Connect: client is closed")
	}

	c.tr = transport.NewAppServer(transport.AppServerConfig{
		CLIPath:        c.opts.CLIPath,
		ExtraArgs:      c.opts.ExtraArgs,
		Env:            c.opts.Env,
		Logger:         c.logger,
		ReadBufferSize: c.opts.ReadBufferSize,
	})
	if err := c.tr.Connect(ctx); err != nil {
		return fmt.Errorf("codex.Client.Connect: transport: %w", err)
	}
	c.demux = c.tr.Demux()

	// Send initialize.
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    c.opts.ClientName,
			"version": c.opts.ClientVersion,
			"title":   c.opts.ClientTitle,
		},
		"capabilities": map[string]any{
			"experimentalApi": false,
		},
	}
	resp, err := c.demux.Send(ctx, "initialize", params)
	if err != nil {
		_ = c.tr.Close(context.Background())
		return fmt.Errorf("codex.Client.Connect: initialize: %w", err)
	}
	if resp.Error != nil {
		_ = c.tr.Close(context.Background())
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	if err := json.Unmarshal(resp.Result, &c.initResult); err != nil {
		c.logger.Warn("codex.Client.Connect: initialize response shape unrecognized",
			zap.Error(err))
	}

	// Send initialized notification.
	if err := c.demux.Notify("initialized", nil); err != nil {
		_ = c.tr.Close(context.Background())
		return fmt.Errorf("codex.Client.Connect: initialized: %w", err)
	}

	// Start dispatcher.
	c.dispatcherCtx, c.dispatcherCancel = context.WithCancel(context.Background())
	c.dispatcherDone = make(chan struct{})
	go c.dispatch()

	c.logger.Debug("codex client connected",
		zap.String("user_agent", c.initResult.UserAgent),
		zap.String("codex_home", c.initResult.CodexHome))
	return nil
}

// InitializeResult returns the response payload received during Connect.
// Only meaningful after Connect returns nil.
func (c *Client) InitializeResult() InitializeResult { return c.initResult }

// Close shuts down the dispatcher, closes every Thread's inbox, and
// terminates the subprocess with the 3-stage graceful-shutdown ladder.
// Safe to call multiple times.
func (c *Client) Close(ctx context.Context) error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.dispatcherCancel != nil {
		c.dispatcherCancel()
	}
	if c.dispatcherDone != nil {
		<-c.dispatcherDone
	}

	c.mu.Lock()
	for _, t := range c.threads {
		t.markClosed()
	}
	c.threads = nil
	c.mu.Unlock()

	if c.tr != nil {
		return c.tr.Close(ctx)
	}
	return nil
}

// dispatch runs the event-routing goroutine. Reads notifications and
// server-initiated requests from the demux and fans them out.
func (c *Client) dispatch() {
	defer close(c.dispatcherDone)
	for {
		select {
		case <-c.dispatcherCtx.Done():
			return
		case note, ok := <-c.demux.Notifications():
			if !ok {
				return
			}
			c.handleNotification(note)
		case sreq, ok := <-c.demux.ServerRequests():
			if !ok {
				return
			}
			c.handleServerRequest(sreq)
		}
	}
}

func (c *Client) handleNotification(n jsonrpc.Notification) {
	ev, err := events.ParseEvent(n)
	if err != nil {
		c.logger.Warn("parse event failed",
			zap.String("method", n.Method),
			zap.Error(err))
		return
	}
	threadID := extractThreadIDFromEvent(ev)
	if threadID == "" {
		// Global events — configWarning, account/rateLimits/updated, etc.
		// Logged at debug; clients that want them must expose a hook in v1.1.
		c.logger.Debug("unroutable event (no thread_id)",
			zap.String("method", ev.EventMethod()))
		return
	}
	c.mu.Lock()
	t := c.threads[threadID]
	c.mu.Unlock()
	if t == nil {
		// Thread may not be registered yet (event arrived before
		// StartThread stored the Thread). Ignore — the spike transcript
		// showed mcpServer/startupStatus/updated arriving before thread/
		// started which we don't route anyway.
		return
	}
	t.deliverEvent(ev)
}

func (c *Client) handleServerRequest(sreq jsonrpc.ServerRequest) {
	req, err := events.ParseApprovalRequest(sreq.Method, sreq.Params)
	if err != nil {
		c.logger.Warn("parse server-request failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
		_ = c.demux.RespondServerRequest(sreq.ID, nil, &jsonrpc.RPCError{
			Code:    -32000,
			Message: "client parse error: " + err.Error(),
		})
		return
	}
	cb := c.opts.ApprovalCallback
	if cb == nil {
		cb = types.DefaultDenyApprovalCallback
	}
	// Use dispatcherCtx so callbacks get canceled on Close.
	decision := cb(c.dispatcherCtx, req)
	result := events.EncodeApprovalDecision(decision)
	if err := c.demux.RespondServerRequest(sreq.ID, result, nil); err != nil {
		c.logger.Warn("approval response write failed",
			zap.String("method", sreq.Method),
			zap.Error(err))
	}
}

// registerThread stores a Thread in the client's routing table. The caller
// must own the thread's ID (i.e., thread/start or thread/resume succeeded).
func (c *Client) registerThread(t *Thread) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		c.threads[t.id] = t
	}
}

// unregisterThread removes a Thread from routing. Called when the thread
// is archived or the caller drops it.
func (c *Client) unregisterThread(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.threads != nil {
		delete(c.threads, threadID)
	}
}

// extractThreadIDFromEvent returns the ThreadID field of every event type
// that carries one. Returns "" for events that don't (e.g., raw
// ErrorEvent, UnknownEvent).
func extractThreadIDFromEvent(ev types.ThreadEvent) string {
	switch e := ev.(type) {
	case *types.ThreadStarted:
		return e.ThreadID
	case *types.TurnStarted:
		return e.ThreadID
	case *types.TurnCompleted:
		return e.ThreadID
	case *types.TurnFailed:
		return e.ThreadID
	case *types.ItemStarted:
		return e.ThreadID
	case *types.ItemUpdated:
		return e.ThreadID
	case *types.ItemCompleted:
		return e.ThreadID
	case *types.TokenUsageUpdated:
		return e.ThreadID
	case *types.ContextCompacted:
		return e.ThreadID
	case *types.HookStarted:
		return e.ThreadID
	case *types.HookCompleted:
		return e.ThreadID
	case *types.ThreadArchived:
		return e.ThreadID
	case *types.ThreadUnarchived:
		return e.ThreadID
	case *types.ThreadClosed:
		return e.ThreadID
	case *types.ThreadNameUpdated:
		return e.ThreadID
	case *types.ThreadStatusChanged:
		return e.ThreadID
	case *types.TurnDiffUpdated:
		return e.ThreadID
	case *types.TurnPlanUpdated:
		return e.ThreadID
	case *types.ItemGuardianApprovalReviewStarted:
		return e.ThreadID
	case *types.ItemGuardianApprovalReviewCompleted:
		return e.ThreadID
	case *types.ModelRerouted:
		return e.ThreadID
	case *types.ServerRequestResolved:
		return e.ThreadID
	case *types.ThreadRealtimeStarted:
		return e.ThreadID
	case *types.ThreadRealtimeClosed:
		return e.ThreadID
	case *types.ThreadRealtimeError:
		return e.ThreadID
	case *types.ThreadRealtimeItemAdded:
		return e.ThreadID
	case *types.ThreadRealtimeOutputAudioDelta:
		return e.ThreadID
	case *types.ThreadRealtimeSdp:
		return e.ThreadID
	case *types.ThreadRealtimeTranscriptDelta:
		return e.ThreadID
	case *types.ThreadRealtimeTranscriptDone:
		return e.ThreadID
	}
	return ""
}
