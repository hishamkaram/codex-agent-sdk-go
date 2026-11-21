package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ====================================================================
// Mutating thread methods (Batch 3 of v0.4.0).
//
// Each maps a slash-command-equivalent app-server JSON-RPC method:
//
//   Rollback                  → thread/rollback                  (≈/rollback)
//   SetName                   → thread/name/set                  (≈ thread metadata)
//   Steer                     → turn/steer                       (≈ in-flight refinement)
//   CleanBackgroundTerminals  → thread/backgroundTerminals/clean (≈/stop)
//
// All wire shapes verified live against codex 0.121.0 (see Batch 1
// probe results in tests/fixtures/v040_probes/).
// ====================================================================

// Rollback removes the last numTurns turns from this thread's history
// and persists a rollback marker. numTurns must be >= 1.
//
// IMPORTANT: rollback only modifies thread history — it does NOT
// revert local file changes the agent made during those turns. Callers
// are responsible for reverting file edits separately (e.g., via
// `git checkout` or the SDK's GitDiff helper).
//
// Wire shape (verified live — codex 0.121.0 errors with `"Invalid
// request: missing field 'numTurns'"` when the param is named
// otherwise):
//
//	{"threadId": "...", "numTurns": <int>}
func (t *Thread) Rollback(ctx context.Context, numTurns int) error {
	if numTurns < 1 {
		return fmt.Errorf("codex.Thread.Rollback: numTurns must be >= 1, got %d", numTurns)
	}
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.Rollback: thread closed")
	}
	_, err := t.client.sendRaw(ctx, "Thread.Rollback", "thread/rollback", map[string]any{
		"threadId": t.id,
		"numTurns": numTurns,
	})
	return err
}

// SetName updates this thread's user-facing name. After the RPC ack
// (which is `{}`), codex emits a `thread/name/updated` notification —
// the SDK already parses that into *types.ThreadNameUpdated.
//
// Wire shape (verified live, returns empty `{}` ack):
//
//	{"threadId": "...", "name": "..."}
func (t *Thread) SetName(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("codex.Thread.SetName: name must not be empty")
	}
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.SetName: thread closed")
	}
	_, err := t.client.sendRaw(ctx, "Thread.SetName", "thread/name/set", map[string]any{
		"threadId": t.id,
		"name":     name,
	})
	return err
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
// (mirrors Thread.Interrupt behavior at threads.go:91-94).
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

// CleanBackgroundTerminals stops every background terminal session
// associated with this thread. Equivalent to TUI `/stop`.
//
// REQUIRES experimentalApi capability — call
// types.NewCodexOptions().WithExperimentalAPI(true) on the Client at
// construction time. Without it, this method returns
// *types.FeatureNotEnabledError.
func (t *Thread) CleanBackgroundTerminals(ctx context.Context) error {
	if t.isClosed() {
		return fmt.Errorf("codex.Thread.CleanBackgroundTerminals: thread closed")
	}
	err := t.client.CleanBackgroundTerminals(ctx, t.id)
	if err != nil && types.IsFeatureNotEnabledError(err) {
		// Augment with concrete remediation advice.
		return fmt.Errorf("%w (set CodexOptions.WithExperimentalAPI(true) at NewClient)", err)
	}
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
