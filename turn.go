package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/commands"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/runtimeprobe"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/session"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadconfig"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ThreadInboxBuffer bounds the queued events of a streamed provider operation.
const ThreadInboxBuffer = threadexec.InboxBuffer

// ErrNoActiveTurn is returned by Thread.Interrupt when no turn is currently
// running. Callers can treat it as a recoverable late interrupt.
var ErrNoActiveTurn = errors.New("codex: no active turn")

// Interrupt cancels the currently-running turn (if any). Sends turn/interrupt
// to the server. Safe to call from any goroutine.
func (t *Thread) Interrupt(ctx context.Context) error {
	if t.record.Closed() {
		return fmt.Errorf("codex.Thread.Interrupt: %w", types.ErrThreadClosed)
	}
	tid := t.record.ActiveID()
	if tid == "" {
		return fmt.Errorf("codex.Thread.Interrupt: %w", ErrNoActiveTurn)
	}
	if err := t.client.InterruptThreadTurn(ctx, t.id, tid); err != nil {
		return fmt.Errorf("codex.Thread.Interrupt: %w", err)
	}
	return nil
}

// RunStreamed sends a user prompt and returns the matching provider events.
// The channel closes on completion, cancellation, or client shutdown. A
// canceled stream closes locally without waiting for provider termination.
// Use the supplied context to distinguish cancellation from completion.
func (t *Thread) RunStreamed(ctx context.Context, prompt string, opts *types.RunOptions) (<-chan types.ThreadEvent, error) {
	events, _, err := t.runStreamed(ctx, prompt, opts)
	return events, err
}

func (t *Thread) runStreamed(ctx context.Context, prompt string, opts *types.RunOptions) (<-chan types.ThreadEvent, *threadexec.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	input, err := threadconfig.BuildTurnInput(prompt, opts)
	if err != nil {
		return nil, nil, err
	}
	if opts != nil && opts.CyberAccessProgram != nil {
		if validationErr := t.client.requireTurnProgram(ctx, *opts.CyberAccessProgram); validationErr != nil {
			return nil, nil, validationErr
		}
	}
	params := threadconfig.BuildTurnStartParams(t.id, input, opts)
	op, _, err := t.beginOperation(ctx, threadexec.Turn, true, "turn/start", params)
	if err != nil {
		return nil, nil, fmt.Errorf("codex.Thread.RunStreamed: turn/start: %w", err)
	}
	return t.record.Stream(ctx, op, t.executionWire()), op, nil
}

// Turn is the buffered result of Thread.Run — all events accumulated
// in order, with convenience fields for final response text and usage.
type Turn struct {
	ID             string
	ThreadID       string
	Status         string
	Items          []types.ThreadItem
	Events         []types.ThreadEvent
	Usage          types.TokenUsage
	UsageAvailable bool   // Usage was measured for this turn, including a measured zero.
	FinalResponse  string // text of the last completed AgentMessage
}

// Run is a buffered convenience wrapper over RunStreamed. It blocks until
// the turn completes (or fails / the context is canceled), accumulating
// every event into a *Turn.
func (t *Thread) Run(ctx context.Context, prompt string, opts *types.RunOptions) (*Turn, error) {
	events, op, err := t.runStreamed(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}
	turn := &Turn{ID: op.AckID(), ThreadID: t.id}
	var turnErr error
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
		case *types.TurnCompleted:
			turn.Status = e.Status
		case *types.TurnFailed:
			turn.Status = "failed"
			turnErr = types.NewRPCError(-1, e.Message, nil)
		}
	}
	usage, available, completed := t.record.Final(op)
	turn.Usage, turn.UsageAvailable = usage, available
	if ctx.Err() != nil {
		return turn, fmt.Errorf("codex.Thread.Run: %w", ctx.Err())
	}
	if !completed {
		return turn, fmt.Errorf("codex.Thread.Run: %w", types.ErrThreadClosed)
	}
	return turn, turnErr
}

// ErrThreadExecutionUnconfirmed means another provider operation may still
// own this thread. The SDK sends no new work until it sees matching completion
// or proves no operation started. A fresh idle resume reply can resolve an
// unchanged external-only fence. An unresolved SDK operation still requires
// terminal evidence or client close. A destination with uncertain approved
// plan preparation should be replaced by a fresh thread.
var ErrThreadExecutionUnconfirmed = errors.New("codex: thread execution is unconfirmed")

// ThreadExecutionUnconfirmedError identifies the affected provider thread.
type ThreadExecutionUnconfirmedError struct {
	ThreadID string
}

func (e *ThreadExecutionUnconfirmedError) Error() string {
	return fmt.Sprintf("%v for thread %q", ErrThreadExecutionUnconfirmed, e.ThreadID)
}

func (*ThreadExecutionUnconfirmedError) Unwrap() error { return ErrThreadExecutionUnconfirmed }

func (t *Thread) beginOperation(ctx context.Context, kind threadexec.Kind, streamed bool, method string, params any) (*threadexec.Operation, jsonrpc.Response, error) {
	if t.isClosed() || t.record == nil {
		return nil, jsonrpc.Response{}, types.ErrThreadClosed
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return nil, jsonrpc.Response{}, err
	}
	return t.record.Begin(ctx, t.id, kind, streamed, method, params, t.executionWire())
}

func (t *Thread) executionWire() threadexec.Wire {
	id := threadexec.CanonicalID(t.id)
	demux := t.client.session.Demux()
	return threadexec.Wire{
		PostGuarded: demux.PostGuarded,
		PendingActivity: func() (bool, error) {
			return session.PendingActivity(demux, id)
		},
		Interrupt: t.client.InterruptThreadTurn,
		Unconfirmed: func(threadID string) error {
			return &ThreadExecutionUnconfirmedError{ThreadID: threadID}
		},
		Warn: func(message string) { t.client.logger.Warn(message) },
	}
}

// Steer appends additional input to the currently-active turn. Use
// to refine or extend the agent's instructions mid-turn (e.g., "also
// run the linter when you're done").
//
// Steer is sync at the RPC layer — codex acknowledges receipt
// immediately. The CONSEQUENCES (new agent message deltas, tool
// calls, etc.) flow through the caller's existing RunStreamed
// channel as additional ItemUpdated/Started events.
//
// Errors with "no active turn" if no turn is currently in flight
// (mirrors Thread.Interrupt behavior).
func (t *Thread) Steer(ctx context.Context, text string) error {
	if text == "" {
		return fmt.Errorf("codex.Thread.Steer: text must not be empty")
	}
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.Steer: thread closed")
	}
	tid := ""
	if t.record != nil {
		tid = t.record.ActiveID()
	}
	if tid == "" {
		return fmt.Errorf("codex.Thread.Steer: no active turn")
	}
	_, err := t.client.sendRaw(ctx, "Thread.Steer", "turn/steer", map[string]any{
		"threadId":       t.id,
		"expectedTurnId": tid,
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	return err
}

// Compact requests that codex summarize the thread's earlier turns. The
// returned CompactResult acknowledges receipt. Wait can observe the legacy
// thread/compacted notification with its own context.
//
// The Client keeps execution reserved until correlated completion. After
// five seconds without confirmation, a new operation returns
// ErrThreadExecutionUnconfirmed. The admission deadline does not bound Wait.
func (t *Thread) Compact(ctx context.Context, opts *types.CompactOptions) (*CompactResult, error) {
	if t.isClosed() {
		return nil, fmt.Errorf("codex.Thread.Compact: %w", types.ErrThreadClosed)
	}
	params := types.CompactStartParams{ThreadID: t.id}
	if opts != nil && opts.Strategy != "" {
		params.Strategy = opts.Strategy
	}
	op, _, err := t.beginOperation(ctx, threadexec.Compact, false, "thread/compact/start", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Thread.Compact: %w", err)
	}
	return &CompactResult{ThreadID: t.id, waiter: t.record.CompactWaiter(op)}, nil
}

// Summarize is sugar for `Compact(ctx, nil)` — a discoverable alias
// for callers who prefer the verb.
func (t *Thread) Summarize(ctx context.Context) (*CompactResult, error) {
	return t.Compact(ctx, nil)
}

// StartReview invokes codex's reviewer against the configured Target.
// Equivalent to TUI `/review`.
//
// The RPC is synchronous in the sense that it returns immediately
// with a ReviewStartResult descriptor — but the actual review OUTPUT
// (item/* notifications) streams in the background on the review
// thread's event channel:
//
//   - Delivery = ReviewInline (or empty default): events stream on
//     this Thread's existing channel. Consume via RunStreamed's
//     return channel or Client's raw notification stream.
//   - Delivery = ReviewDetached: ReviewThreadID is a NEW thread id.
//     The caller should ResumeThread(ctx, ReviewThreadID) to create
//     a second Thread handle for observing the review's events.
//
// Target is REQUIRED — use one of the ReviewTarget* constructors.
func (t *Thread) StartReview(ctx context.Context, opts types.ReviewOptions) (*types.ReviewStartResult, error) {
	if t.isClosed() {
		return nil, fmt.Errorf("codex.Thread.StartReview: %w", types.ErrThreadClosed)
	}
	if opts.Target.Type == "" {
		return nil, fmt.Errorf("codex.Thread.StartReview: opts.Target.Type is required (use ReviewTarget* constructors)")
	}
	params := types.ReviewStartParams{ThreadID: t.id, Target: opts.Target, Delivery: opts.Delivery}
	var result json.RawMessage
	var err error
	if opts.Delivery == types.ReviewDetached {
		if reentryErr := t.client.checkApprovalReentry(ctx); reentryErr != nil {
			return nil, reentryErr
		}
		result, err = t.client.sendRaw(ctx, "Thread.StartReview", "review/start", params)
	} else {
		_, reply, startErr := t.beginOperation(ctx, threadexec.Review, false, "review/start", params)
		result, err = reply.Result, startErr
	}
	if err != nil {
		return nil, fmt.Errorf("codex.Thread.StartReview: %w", err)
	}
	var review types.ReviewStartResult
	if err := json.Unmarshal(result, &review); err != nil {
		return nil, fmt.Errorf("codex.Thread.StartReview: decode response: %w", err)
	}
	return &review, nil
}

// StartReviewStreamed invokes codex's reviewer on the current thread and
// returns the review event stream. It is the review/start equivalent of
// RunStreamed: the caller owns the returned channel until the review turn
// emits TurnCompleted or TurnFailed, or ctx is canceled.
//
// Only inline delivery is streamable through this thread handle. Detached
// review creates another thread; callers that need detached review output must
// use StartReview and explicitly attach to the returned ReviewThreadID.
func (t *Thread) StartReviewStreamed(ctx context.Context, opts types.ReviewOptions) (<-chan types.ThreadEvent, error) {
	if t.isClosed() {
		return nil, fmt.Errorf("codex.Thread.StartReviewStreamed: thread closed")
	}
	if opts.Target.Type == "" {
		return nil, fmt.Errorf("codex.Thread.StartReviewStreamed: opts.Target.Type is required (use ReviewTarget* constructors)")
	}
	if opts.Delivery == types.ReviewDetached {
		return nil, fmt.Errorf("codex.Thread.StartReviewStreamed: detached review delivery is not streamable from the current thread")
	}

	params := types.ReviewStartParams{ThreadID: t.id, Target: opts.Target, Delivery: opts.Delivery}
	op, _, err := t.beginOperation(ctx, threadexec.Review, true, "review/start", params)
	if err != nil {
		return nil, fmt.Errorf("codex.Thread.StartReviewStreamed: %w", err)
	}
	return t.record.Stream(ctx, op, t.executionWire()), nil
}

// CompactResult is returned by Thread.Compact. The `thread/compact/start`
// RPC is async — codex acknowledges the request immediately but the
// actual compaction happens in the background and is signaled by a
// `thread/compacted` notification.
//
// The hybrid pattern: callers can fire-and-forget (just let the result
// be GC'd) OR block for completion via Wait. Mirrors the
// `exec.Cmd.Start()` + `exec.Cmd.Wait()` pattern.
//
// Internally, CompactResult wraps a one-shot channel the Thread's
// dispatcher fills when the `*types.ContextCompacted` event arrives.
// The subscription is installed BEFORE the RPC is sent so the
// notification can never arrive before the receiver is ready.
type CompactResult struct {
	// ThreadID is the thread the compaction was requested on.
	ThreadID string

	waiter threadexec.CompactWaiter
}

// Wait blocks until the matching `thread/compacted` notification
// arrives, ctx is canceled, or the Thread closes. Returns the parsed
// event or the ctx error.
//
// Wait is idempotent: subsequent calls return the same event (the
// subscription is drained on the first call and cached).
func (r *CompactResult) Wait(ctx context.Context) (*types.ContextCompacted, error) {
	if r == nil {
		return nil, fmt.Errorf("codex.CompactResult.Wait: nil result")
	}
	return r.waiter.Wait(ctx)
}

const prepareApprovedPlanMethod = runtimeprobe.PrepareApprovedPlanMethod

// PrepareApprovedPlan prepares approved Markdown on a fresh destination
// thread and returns the provider's first-turn prompt. Keep the destination
// for exclusive use through the first Run. The source thread is not involved.
//
// This experimental method requires WithExperimentalAPI(true) and proof from
// the connected native Linux executable. Conflicting activity, such as a turn
// in flight, can cause [ErrThreadExecutionUnconfirmed] before the SDK sends
// the preparation request.
//
// A failed preparation can leave destination history changed if the request
// was sent. Preserve the approved Markdown and use a fresh destination for
// any retry after a sent preparation or first-turn startup fails.
func (t *Thread) PrepareApprovedPlan(ctx context.Context, markdown string, opts *types.PrepareApprovedPlanOptions) (*types.PreparedApprovedPlan, error) {
	const caller = "codex.Thread.PrepareApprovedPlan"
	if ctx == nil {
		return nil, fmt.Errorf("%s: context is required", caller)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	if t.record == nil || t.isClosed() {
		return nil, fmt.Errorf("%s: %w", caller, types.ErrThreadClosed)
	}
	if err := t.client.checkApprovalReentry(ctx); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	attachment := ""
	if opts != nil {
		attachment = opts.AttachmentPath
	}
	if err := threadexec.ValidateApprovedPlan(markdown, attachment); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	if err := t.client.requireConnectedSchema(ctx, connectedSchemaFeature{
		method: prepareApprovedPlanMethod, name: "approved plan preparation", supported: runtimeprobe.SupportsApprovedPlan,
	}); err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	prepared, rpcErr, err := t.record.PrepareApprovedPlan(ctx, prepareApprovedPlanMethod, threadexec.ApprovedPlanParams{
		ThreadID: t.id, Markdown: markdown, AttachmentPath: attachment,
	}, t.executionWire())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", caller, err)
	}
	if rpcErr != nil {
		return nil, fmt.Errorf("%s: %w", caller, classifyRPCError(prepareApprovedPlanMethod, rpcErr))
	}
	return prepared, nil
}

// InterruptThreadTurn asks the provider to interrupt the exact turn on the
// exact thread. The spawning parent may remain blocked waiting for that child;
// callers must observe both child and parent lifecycle before presenting this
// as delegated-agent cancellation.
func (c *Client) InterruptThreadTurn(ctx context.Context, threadID, turnID string) error {
	return commands.InterruptThreadTurn(ctx, c.sendRaw, threadID, turnID)
}
