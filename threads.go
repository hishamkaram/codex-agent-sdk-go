package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// ThreadInboxBuffer is the size of each Thread's internal event inbox.
// Events flow: dispatcher → Thread.inbox → per-Run filter goroutine →
// caller's channel. 256 accommodates bursty streaming without blocking the
// dispatcher for realistic turn sizes.
const ThreadInboxBuffer = 256

// Thread is a handle to a single codex conversation. Construct via
// Client.StartThread or Client.ResumeThread. A Thread is NOT safe for
// concurrent Run/RunStreamed calls — use a sync.Mutex on the caller side
// if you need that, or create a second Thread.
//
// Internally Run serializes concurrent calls via a per-thread turnMu.
// Concurrent turns on the same thread collapse their event boundaries
// server-side; client serialization is mandatory.
type Thread struct {
	client *Client
	id     string
	// cwd is the working directory the thread was started with.
	// Populated by StartThread / ResumeThread / ForkThread from
	// ThreadOptions.Cwd (falls back to Client.opts.DefaultCwd).
	// Used by local-only helpers like Thread.GitDiff; empty string
	// means "no cwd known" — helpers that need one will error.
	cwd string

	turnMu sync.Mutex
	inbox  chan types.ThreadEvent

	closed atomic.Bool

	// activeTurnID holds the most recent turn/started id observed during
	// the current Run. Used by Interrupt to fire turn/interrupt.
	activeTurnID atomic.Value // string

	// compactSub is a one-shot subscription for *types.ContextCompacted
	// events, installed by Thread.Compact BEFORE its RPC is sent so
	// the notification can't race the subscription. Nil when no
	// Compact is in flight. See compact_result.go.
	compactSub atomic.Pointer[chan *types.ContextCompacted]
}

// newThread constructs a Thread owned by c. Does not register it; caller
// must call c.registerThread.
func newThread(c *Client, id string) *Thread {
	t := &Thread{
		client: c,
		id:     id,
		inbox:  make(chan types.ThreadEvent, ThreadInboxBuffer),
	}
	t.activeTurnID.Store("")
	return t
}

// ID returns the server-assigned thread identifier.
func (t *Thread) ID() string { return t.id }

// deliverEvent pushes an event to the thread's inbox. Called from the
// Client's dispatcher goroutine. Never blocks — drops events if the inbox
// is full, which only happens if the caller stalls consuming the channel.
//
// Tees *types.ContextCompacted to any installed compactSub BEFORE
// pushing to inbox, so a running RunStreamed consumer still observes
// the event normally. The tee is non-blocking (select-default) — a
// caller that forgot to Wait does not stall the dispatcher.
func (t *Thread) deliverEvent(ev types.ThreadEvent) {
	if t.closed.Load() {
		return
	}
	// Track the active turn ID for Interrupt.
	if ts, ok := ev.(*types.TurnStarted); ok {
		t.activeTurnID.Store(ts.TurnID)
	}
	// Tee to any installed compact subscription.
	if cc, ok := ev.(*types.ContextCompacted); ok {
		if sub := t.compactSub.Load(); sub != nil {
			select {
			case *sub <- cc:
			default:
				// Subscriber buffer full or gone — skip.
			}
		}
	}
	select {
	case t.inbox <- ev:
	default:
		t.client.logger.Warn("thread inbox full, dropping event",
			zap.String("thread_id", t.id),
			zap.String("method", ev.EventMethod()))
	}
}

// markClosed signals that the thread should stop accepting new events.
// Idempotent. Does NOT close the inbox channel (dispatcher may still be
// writing; closer coordinates with Run's filter goroutine).
//
// Signals any pending Compact.Wait by closing the subscription
// channel — Wait observes the close via the zero-value receive.
func (t *Thread) markClosed() {
	if !t.closed.CompareAndSwap(false, true) {
		return // already closed
	}
	if sub := t.compactSub.Swap(nil); sub != nil {
		close(*sub)
	}
}

// Interrupt cancels the currently-running turn (if any). Sends turn/interrupt
// to the server. Safe to call from any goroutine.
func (t *Thread) Interrupt(ctx context.Context) error {
	if t.closed.Load() {
		return fmt.Errorf("codex.Thread.Interrupt: thread closed")
	}
	tid, _ := t.activeTurnID.Load().(string)
	if tid == "" {
		return fmt.Errorf("codex.Thread.Interrupt: no active turn")
	}
	resp, err := t.client.demux.Send(ctx, "turn/interrupt", map[string]any{
		"threadId": t.id,
		"turnId":   tid,
	})
	if err != nil {
		return fmt.Errorf("codex.Thread.Interrupt: %w", err)
	}
	if resp.Error != nil {
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	return nil
}

// RunStreamed sends a user prompt and returns a channel of events produced
// during the turn. The channel closes after a TurnCompleted (or TurnFailed)
// for the current turn is observed, or on error/ctx-cancel.
//
// Concurrent RunStreamed calls on the same thread serialize via turnMu —
// later calls block until the earlier one's channel is fully drained and
// its internal goroutine releases the lock.
func (t *Thread) RunStreamed(ctx context.Context, prompt string, opts *types.RunOptions) (<-chan types.ThreadEvent, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("codex.Thread.RunStreamed: thread closed")
	}

	t.turnMu.Lock()
	unlockOnError := true
	defer func() {
		if unlockOnError {
			t.turnMu.Unlock()
		}
	}()

	input, err := buildTurnInput(prompt, opts)
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"threadId": t.id,
		"input":    input,
	}
	if opts != nil && opts.OutputSchema != nil {
		params["outputSchema"] = opts.OutputSchema
	}

	resp, err := t.client.demux.Send(ctx, "turn/start", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Thread.RunStreamed: turn/start: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}

	// turn/start response typically carries {"turn":{"id":"..."}}. Best
	// effort — if we don't get a turnID from the response, the turn/started
	// notification will set it via deliverEvent.
	var startResp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(resp.Result, &startResp)
	if startResp.Turn.ID != "" {
		t.activeTurnID.Store(startResp.Turn.ID)
	}

	out := make(chan types.ThreadEvent, ThreadInboxBuffer)
	unlockOnError = false
	go t.streamLoop(ctx, startResp.Turn.ID, out)
	return out, nil
}

// streamLoop is the per-Run filter goroutine. Forwards events from the
// thread's inbox into the caller's channel until TurnCompleted or
// TurnFailed matches the current turnID. Releases turnMu on exit.
func (t *Thread) streamLoop(ctx context.Context, expectedTurnID string, out chan<- types.ThreadEvent) {
	defer t.turnMu.Unlock()
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-t.inbox:
			if !ok {
				return
			}
			// Forward everything to the caller.
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
			// Detect turn terminus. If we don't yet know the turnID from
			// the turn/start response, use the turn/started notification.
			if expectedTurnID == "" {
				if ts, ok := ev.(*types.TurnStarted); ok {
					expectedTurnID = ts.TurnID
				}
			}
			if isTurnTerminus(ev, expectedTurnID) {
				return
			}
		}
	}
}

// isTurnTerminus returns true if ev is TurnCompleted or TurnFailed whose
// TurnID matches expected (or if expected is empty, any terminal event).
func isTurnTerminus(ev types.ThreadEvent, expected string) bool {
	switch e := ev.(type) {
	case *types.TurnCompleted:
		return expected == "" || e.TurnID == expected
	case *types.TurnFailed:
		return expected == "" || e.TurnID == expected
	}
	return false
}

// Turn is the buffered result of Thread.Run — all events accumulated
// in order, with convenience fields for final response text and usage.
type Turn struct {
	ID            string
	ThreadID      string
	Status        string
	Items         []types.ThreadItem
	Events        []types.ThreadEvent
	Usage         types.TokenUsage
	FinalResponse string // concatenation of every AgentMessage content
}

// Run is a buffered convenience wrapper over RunStreamed. It blocks until
// the turn completes (or fails / the context is canceled), accumulating
// every event into a *Turn.
func (t *Thread) Run(ctx context.Context, prompt string, opts *types.RunOptions) (*Turn, error) {
	events, err := t.RunStreamed(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}
	turn := &Turn{ThreadID: t.id}
	// Track the latest usage snapshot seen on this thread — codex emits
	// usage via thread/tokenUsage/updated notifications, NOT on
	// turn/completed directly. We assign the latest snapshot to
	// turn.Usage when the turn terminates.
	var latestUsage types.TokenUsage
	for ev := range events {
		turn.Events = append(turn.Events, ev)
		switch e := ev.(type) {
		case *types.TurnStarted:
			turn.ID = e.TurnID
		case *types.ItemCompleted:
			turn.Items = append(turn.Items, e.Item)
			if msg, ok := e.Item.(*types.AgentMessage); ok {
				turn.FinalResponse = msg.Text
			}
		case *types.TokenUsageUpdated:
			latestUsage = e.Usage
		case *types.TurnCompleted:
			turn.Status = e.Status
			// Prefer usage on turn/completed (flat shape, forward-compat)
			// but fall back to the latest tokenUsage snapshot.
			if e.Usage != (types.TokenUsage{}) {
				turn.Usage = e.Usage
			} else {
				turn.Usage = latestUsage
			}
		case *types.TurnFailed:
			turn.Status = "failed"
			return turn, types.NewRPCError(-1, e.Message, nil)
		}
	}
	// If we exited without TurnCompleted (e.g., ctx cancel) but saw
	// usage snapshots, surface them anyway.
	if turn.Usage == (types.TokenUsage{}) && latestUsage != (types.TokenUsage{}) {
		turn.Usage = latestUsage
	}
	if ctx.Err() != nil {
		return turn, fmt.Errorf("codex.Thread.Run: %w", ctx.Err())
	}
	return turn, nil
}

// buildTurnInput constructs the "input" array for turn/start. Spike
// transcript shape:
//
//	[{"type":"text","text":"..."},
//	 {"type":"skill","name":"skill-name","path":"/abs/SKILL.md"},
//	 {"type":"localImage","path":"/abs/path.png"}]
func buildTurnInput(prompt string, opts *types.RunOptions) ([]map[string]any, error) {
	out := []map[string]any{{"type": "text", "text": prompt}}
	if opts != nil {
		for _, skill := range opts.Skills {
			if skill.Name == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: skill name is empty")
			}
			if skill.Path == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: skill path is empty")
			}
			out = append(out, map[string]any{
				"type": "skill",
				"name": skill.Name,
				"path": skill.Path,
			})
		}
		for _, path := range opts.Images {
			if path == "" {
				return nil, fmt.Errorf("codex.buildTurnInput: image path is empty")
			}
			out = append(out, map[string]any{
				"type": "localImage",
				"path": path,
			})
		}
	}
	return out, nil
}

// StartThread sends thread/start and returns a new Thread. The thread is
// registered in the client's routing table.
func (c *Client) StartThread(ctx context.Context, opts *types.ThreadOptions) (*Thread, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.StartThread: client not connected or already closed")
	}
	params := buildThreadStartParams(c.opts, opts)
	resp, err := c.demux.Send(ctx, "thread/start", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.StartThread: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	id, err := extractThreadID(resp.Result)
	if err != nil {
		return nil, err
	}
	t := newThread(c, id)
	t.cwd = resolveCwd(c, opts)
	c.registerThread(t)
	return t, nil
}

// ResumeThread sends thread/resume with optional cwd override and returns
// a new Thread handle for the resumed conversation.
func (c *Client) ResumeThread(ctx context.Context, threadID string, opts *types.ResumeOptions) (*Thread, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.ResumeThread: client not connected or already closed")
	}
	if threadID == "" {
		return nil, fmt.Errorf("codex.Client.ResumeThread: threadID must not be empty")
	}
	params := map[string]any{"threadId": threadID}
	cwd := ""
	if opts != nil && opts.Cwd != "" {
		params["cwd"] = opts.Cwd
		cwd = opts.Cwd
	}
	resp, err := c.demux.Send(ctx, "thread/resume", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ResumeThread: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	// Prefer the ID from the response; fall back to what the caller passed.
	id, err := extractThreadID(resp.Result)
	if err != nil || id == "" {
		id = threadID
	}
	t := newThread(c, id)
	if cwd == "" {
		cwd = c.opts.DefaultCwd
	}
	t.cwd = cwd
	c.registerThread(t)
	return t, nil
}

// resolveCwd picks the Thread's cwd from ThreadOptions, then the
// Client's DefaultCwd, then "" (unknown).
func resolveCwd(c *Client, opts *types.ThreadOptions) string {
	if opts != nil && opts.Cwd != "" {
		return opts.Cwd
	}
	return c.opts.DefaultCwd
}

// Cwd returns the working directory this thread was started with.
// Empty string means the cwd was never set (rare — occurs only when
// both ThreadOptions.Cwd and CodexOptions.DefaultCwd were empty).
func (t *Thread) Cwd() string { return t.cwd }

// ListThreads returns the persisted thread catalog. Best-effort parsing —
// unknown fields are dropped silently.
func (c *Client) ListThreads(ctx context.Context) ([]types.ThreadInfo, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.ListThreads: client not connected or already closed")
	}
	resp, err := c.demux.Send(ctx, "thread/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("codex.Client.ListThreads: %w", err)
	}
	if resp.Error != nil {
		return nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	var out struct {
		Threads []threadListEntry `json:"threads"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, types.NewJSONDecodeError(string(resp.Result), err)
	}
	infos := make([]types.ThreadInfo, 0, len(out.Threads))
	for _, e := range out.Threads {
		infos = append(infos, types.ThreadInfo{
			ThreadID:     e.ID,
			Cwd:          e.Cwd,
			Model:        e.Model,
			Summary:      e.Preview,
			LastModified: e.Path,
			Archived:     e.Archived,
		})
	}
	return infos, nil
}

// ForkThread branches from an existing thread, returning a new Thread whose
// history is seeded with the source thread. The source thread is
// unaffected. Uses "thread/fork" — if the server doesn't expose this verb
// in the current CLI version, the error is returned as-is.
func (c *Client) ForkThread(ctx context.Context, sourceThreadID string, opts *types.ThreadOptions) (*Thread, *types.ForkResult, error) {
	if !c.connected.Load() || c.closed.Load() {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: client not connected or already closed")
	}
	if sourceThreadID == "" {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: sourceThreadID must not be empty")
	}
	params := buildThreadStartParams(c.opts, opts)
	params["sourceThreadId"] = sourceThreadID
	resp, err := c.demux.Send(ctx, "thread/fork", params)
	if err != nil {
		return nil, nil, fmt.Errorf("codex.Client.ForkThread: %w", err)
	}
	if resp.Error != nil {
		return nil, nil, types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	newID, err := extractThreadID(resp.Result)
	if err != nil {
		return nil, nil, err
	}
	t := newThread(c, newID)
	t.cwd = resolveCwd(c, opts)
	c.registerThread(t)
	return t, &types.ForkResult{SourceThreadID: sourceThreadID, NewThreadID: newID}, nil
}

// ArchiveThread marks a thread as archived on the server and unregisters
// it locally.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	if !c.connected.Load() || c.closed.Load() {
		return fmt.Errorf("codex.Client.ArchiveThread: client not connected or already closed")
	}
	resp, err := c.demux.Send(ctx, "thread/archive", map[string]any{"threadId": threadID})
	if err != nil {
		return fmt.Errorf("codex.Client.ArchiveThread: %w", err)
	}
	if resp.Error != nil {
		return types.NewRPCError(resp.Error.Code, resp.Error.Message, resp.Error.Data)
	}
	c.unregisterThread(threadID)
	return nil
}

// buildThreadStartParams merges client-level defaults with per-call
// overrides into the thread/start params payload.
func buildThreadStartParams(clientOpts *types.CodexOptions, opts *types.ThreadOptions) map[string]any {
	p := map[string]any{}
	// Apply client defaults first.
	if clientOpts.DefaultModel != "" {
		p["model"] = clientOpts.DefaultModel
	}
	if clientOpts.DefaultCwd != "" {
		p["cwd"] = clientOpts.DefaultCwd
	}
	if clientOpts.DefaultSandbox != "" {
		p["sandbox"] = string(clientOpts.DefaultSandbox)
	}
	if clientOpts.DefaultApprovalPolicy != "" {
		p["approvalPolicy"] = string(clientOpts.DefaultApprovalPolicy)
	}
	// Per-call overrides win.
	if opts != nil {
		if opts.Model != "" {
			p["model"] = opts.Model
		}
		if opts.Cwd != "" {
			p["cwd"] = opts.Cwd
		}
		if opts.Sandbox != "" {
			p["sandbox"] = string(opts.Sandbox)
		}
		if opts.ApprovalPolicy != "" {
			p["approvalPolicy"] = string(opts.ApprovalPolicy)
		}
	}
	return p
}

// extractThreadID pulls thread.id from a thread/start or thread/resume or
// thread/fork response. Accepts both nested ("thread.id") and flat
// ("threadId") shapes.
func extractThreadID(result json.RawMessage) (string, error) {
	var shape struct {
		ThreadID string `json:"threadId"`
		Thread   *struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &shape); err != nil {
		return "", types.NewJSONDecodeError(string(result), err)
	}
	if shape.ThreadID != "" {
		return shape.ThreadID, nil
	}
	if shape.Thread != nil && shape.Thread.ID != "" {
		return shape.Thread.ID, nil
	}
	return "", types.NewMessageParseError("thread response missing thread id", string(result))
}

// threadListEntry is the shape of each row in a thread/list response.
// Fields match the spike-transcript shape; unknown fields are dropped.
type threadListEntry struct {
	ID       string `json:"id"`
	Cwd      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
	Preview  string `json:"preview,omitempty"`
	Path     string `json:"path,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}

// Compile-time check that Thread exposes the stdlib-expected surface.
var _ = (&Thread{}).ID

// Guard against jsonrpc import being unused if features shrink.
var _ = jsonrpc.ErrClosed
