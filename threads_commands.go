package codex

import (
	"context"
	"encoding/json"
	"fmt"

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
	if t.closed.Load() {
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
	if t.closed.Load() {
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
	if t.closed.Load() {
		return fmt.Errorf("codex.Thread.Steer: thread closed")
	}
	tid, _ := t.activeTurnID.Load().(string)
	if tid == "" {
		return fmt.Errorf("codex.Thread.Steer: no active turn")
	}
	_, err := t.client.sendRaw(ctx, "Thread.Steer", "turn/steer", map[string]any{
		"threadId": t.id,
		"turnId":   tid,
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
	if t.closed.Load() {
		return fmt.Errorf("codex.Thread.CleanBackgroundTerminals: thread closed")
	}
	_, err := t.client.sendRaw(ctx, "Thread.CleanBackgroundTerminals", "thread/backgroundTerminals/clean", map[string]any{
		"threadId": t.id,
	})
	if err != nil && types.IsFeatureNotEnabledError(err) {
		// Augment with concrete remediation advice.
		return fmt.Errorf("%w (set CodexOptions.WithExperimentalAPI(true) at NewClient)", err)
	}
	return err
}

// Compact requests that codex summarize the thread's earlier turns
// into a concise handoff, freeing context for future turns. Equivalent
// to TUI `/compact`.
//
// Compact is hybrid sync/async: the RPC acknowledges receipt
// immediately (empty `{}`), but the actual compaction runs in the
// background and is signaled by a `thread/compacted` notification.
// The returned *CompactResult lets callers EITHER fire-and-forget
// (just ignore the result) OR block via CompactResult.Wait(ctx).
//
// Compact pre-installs a one-shot subscription on the Thread BEFORE
// sending the RPC, so the `*types.ContextCompacted` notification
// cannot race the receiver. The notification is ALSO delivered to
// the normal inbox so any running RunStreamed observer still sees
// it.
//
// UPSTREAM QUIRK (codex 0.121.0, verified live 2026-04-19):
// thread/compact/start reliably ACKS the RPC, but codex does NOT
// reliably emit the `thread/compacted` notification that Wait
// depends on. On populated threads the notification has been
// observed to never arrive — and after ~30s, the subprocess
// transport has been observed to enter an unresponsive state
// ("write: file already closed" on subsequent RPCs). Callers that
// need strict end-to-end compaction should EITHER use fire-and-forget
// (ignore the *CompactResult) OR set a short Wait timeout and treat
// timeout as "might have worked." This is not an SDK bug — it's an
// upstream gap tracked for v0.4.x follow-up.
//
// On a brand-new thread with no rollout file, thread/compact/start
// returns `{}` cleanly (no error) but no notification ever fires.
// Compact on empty threads is effectively a no-op.
//
// Only one Compact may be in flight at a time per Thread. Calling
// Compact while a prior CompactResult has not yet been drained by
// Wait returns an error.
func (t *Thread) Compact(ctx context.Context, opts *types.CompactOptions) (*CompactResult, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("codex.Thread.Compact: thread closed")
	}

	// Install the subscription BEFORE sending the RPC. Buffer of 1
	// so the dispatcher's tee never blocks (even if caller is slow
	// to call Wait).
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	if !t.compactSub.CompareAndSwap(nil, chPtr) {
		return nil, fmt.Errorf("codex.Thread.Compact: another compaction already in flight")
	}

	params := types.CompactStartParams{ThreadID: t.id}
	if opts != nil && opts.Strategy != "" {
		params.Strategy = opts.Strategy
	}

	_, err := t.client.sendRaw(ctx, "Thread.Compact", "thread/compact/start", params)
	if err != nil {
		// RPC failed — detach the subscription so the next Compact
		// attempt can install its own.
		t.compactSub.CompareAndSwap(chPtr, nil)
		return nil, err
	}
	return &CompactResult{
		ThreadID: t.id,
		thread:   t,
		chPtr:    chPtr,
	}, nil
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
	if t.closed.Load() {
		return nil, fmt.Errorf("codex.Thread.StartReview: thread closed")
	}
	if opts.Target.Type == "" {
		return nil, fmt.Errorf("codex.Thread.StartReview: opts.Target.Type is required (use ReviewTarget* constructors)")
	}
	params := types.ReviewStartParams{
		ThreadID: t.id,
		Target:   opts.Target,
		Delivery: opts.Delivery,
	}
	resp, err := t.client.sendRaw(ctx, "Thread.StartReview", "review/start", params)
	if err != nil {
		return nil, err
	}
	var result types.ReviewStartResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Thread.StartReview: decode response: %w", err)
	}
	return &result, nil
}
