package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_CancelBeforeAcknowledgementInterruptsOwnedTurn(t *testing.T) {
	t.Parallel()
	ctx, watchdog := context.WithTimeout(t.Context(), 8*time.Second)
	defer watchdog()
	turnRequest := make(chan jsonrpc.Request, 1)
	interrupt := make(chan jsonrpc.Request, 1)
	acknowledge := make(chan struct{})
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start", "thread/resume":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"idle"}}}`)}
		case "turn/start":
			turnRequest <- req
			select {
			case <-acknowledge:
			case <-ctx.Done():
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"U","rootTurnId":"U"}}`)}
		case "turn/interrupt":
			interrupt <- req
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := c.ResumeThread(ctx, thread.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := thread.Run(turnCtx, "hold acknowledgement", nil)
		done <- runErr
	}()
	awaitExecutionTest(t, ctx, turnRequest)
	peer.push(notify("turn/started", map[string]any{"threadId": "T", "turn": map[string]string{"id": "U", "rootTurnId": "U"}}))
	cancel()
	if err := awaitExecutionTest(t, ctx, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	close(acknowledge)
	req := awaitExecutionTest(t, ctx, interrupt)
	var ids map[string]string
	if err := json.Unmarshal(req.Params, &ids); err != nil || ids["threadId"] != "T" || ids["turnId"] != "U" {
		t.Fatalf("interrupt params = %s, decode error = %v", req.Params, err)
	}

	// An accepted interrupt is not the terminal. A duplicate handle cannot
	// submit while the original reservation is still active.
	wait, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, err := duplicate.Run(wait, "must stay queued", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("duplicate Run error = %v, want deadline", err)
	}
	select {
	case req := <-turnRequest:
		t.Fatalf("duplicate handle sent work before terminal: %s", req.Params)
	default:
	}
	peer.push(notify("turn/completed", map[string]any{"threadId": "T", "turnId": "U", "status": "interrupted"}))
}

func TestClient_TerminalBeforeReplySelectsMatchingTurn(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	start := make(chan struct{})
	reply := make(chan struct{})
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "turn/start":
			close(start)
			select {
			case <-reply:
			case <-ctx.Done():
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"current"}}`)}
		default:
			t.Errorf("unexpected method: %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		turn *Turn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		turn, err := thread.Run(ctx, "two terminal events before acknowledgement", nil)
		done <- result{turn: turn, err: err}
	}()
	awaitExecutionTest(t, ctx, start)
	peer.push(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"T","turnId":"stale","status":"failed"}}`))
	peer.push(json.RawMessage(`{"method":"turn/started","params":{"threadId":"T","turn":{"id":"current","rootTurnId":"current"}}}`))
	peer.push(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"T","turnId":"current","status":"completed","usage":{}}}`))
	close(reply)
	got := awaitExecutionTest(t, ctx, done)
	if got.err != nil || got.turn.ID != "current" || got.turn.Status != "completed" ||
		!got.turn.UsageAvailable || got.turn.Usage != (types.TokenUsage{}) {
		t.Fatalf("matching terminal result = %+v, err = %v", got.turn, got.err)
	}
}

func TestClient_HeldApprovalKeepsDispatching(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	entered := make(chan struct{})
	release := make(chan struct{})
	reentry := make(chan error, 1)
	var sibling *Thread
	opts := types.NewCodexOptions().WithApprovalCallback(func(callbackCtx context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		_, err := sibling.Run(callbackCtx, "recursive work", nil)
		reentry <- err
		close(entered)
		select {
		case <-release:
		case <-callbackCtx.Done():
		}
		return types.ApprovalDeny{}
	})
	c, peer := setupMockClient(t, opts, func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"sibling"}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"sibling-turn"}}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	var err error
	sibling, err = c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(json.RawMessage(`{"id":"held","method":"item/permissions/requestApproval","params":{"threadId":"other","turnId":"held-turn","itemId":"i","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`))
	awaitExecutionTest(t, ctx, entered)
	if reentryErr := awaitExecutionTest(t, ctx, reentry); !errors.Is(reentryErr, ErrApprovalCallbackReentry) {
		t.Fatalf("callback reentry error = %v", reentryErr)
	}
	out, err := sibling.RunStreamed(ctx, "sibling work", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/started", map[string]string{"threadId": "sibling", "turnId": "sibling-turn"}))
	peer.push(notify("turn/completed", map[string]string{"threadId": "sibling", "turnId": "sibling-turn", "status": "completed"}))
	completed := false
	for ev := range out {
		if terminal, ok := ev.(*types.TurnCompleted); ok && terminal.TurnID == "sibling-turn" {
			completed = true
		}
	}
	if !completed {
		t.Fatal("sibling never completed while the callback was held")
	}
	close(release)
	assertApprovalReply(t, peer, `"held"`, `{"permissions":{},"scope":"turn"}`)
}

func awaitExecutionTest[T any](t *testing.T, ctx context.Context, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-ctx.Done():
		t.Fatalf("wire rendezvous: %v", ctx.Err())
		var zero T
		return zero
	}
}

func TestClient_ResumeUnknownActiveRejectsStaleTerminal(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/resume":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"resumed","status":{"type":"active"}}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"new"}}`)}
		}
		t.Errorf("unexpected provider method %s", req.Method)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	thread, err := c.ResumeThread(ctx, "resumed", nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "stale"}))
	awaitExecutionTest(t, ctx, observed)
	if _, err := thread.RunStreamed(ctx, "would steer external work", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("stale terminal released unresolved external work: %v", err)
	}
	peer.push(notify("turn/started", map[string]string{"threadId": "resumed", "turnId": "actual"}))
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "actual", "status": "completed"}))
	awaitExecutionTest(t, ctx, observed)
	awaitExecutionTest(t, ctx, observed)

	out, startErr := thread.RunStreamed(ctx, "after matched external completion", nil)
	if startErr != nil {
		t.Fatal(startErr)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "new", "status": "completed"}))
	terminal, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted)
	if !ok || terminal.TurnID != "new" {
		t.Fatalf("new terminal = %#v", terminal)
	}
}

func TestClient_CompactAdmissionExpiresWithoutEndingWait(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 12*time.Second)
	defer stop()
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "thread/compact/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
		default:
			t.Errorf("unexpected provider method %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := thread.Compact(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thread.RunStreamed(ctx, "silent compact has not finished", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("admission after silent compact = %v", err)
	}
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "compact"}))
	peer.push(json.RawMessage(`{"method":"item/completed","params":{"threadId":"T","turnId":"compact","item":{"type":"contextCompaction","id":"item"}}}`))
	peer.push(notify("thread/compacted", map[string]string{"threadId": "T", "turnId": "compact"}))
	if event, err := result.Wait(ctx); err != nil || event.TurnID != "compact" {
		t.Fatalf("late CompactResult.Wait = %+v, %v", event, err)
	}
	if _, err := thread.RunStreamed(ctx, "compaction item is not a terminal", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("admission before compact terminal = %v", err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "compact", "status": "completed"}))
	for range 4 {
		awaitExecutionTest(t, ctx, observed)
	}
	out, startErr := thread.RunStreamed(ctx, "after compact terminal", nil)
	if startErr != nil {
		t.Fatal(startErr)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "next", "status": "completed"}))
	if _, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted); !ok {
		t.Fatal("new operation did not receive its terminal")
	}
}

func TestClient_CancelBeforeDispatchLeavesConnectionReady(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method != "thread/start" {
			t.Errorf("pre-canceled call sent %s", req.Method)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := thread.Run(runCtx, "must never dispatch", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if _, err := c.StartThread(ctx, nil); err != nil {
		t.Fatalf("client unusable after pre-dispatch cancellation: %v", err)
	}
}
