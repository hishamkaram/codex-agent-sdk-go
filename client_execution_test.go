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
	ctx, watchdog := context.WithTimeout(t.Context(), 12*time.Second)
	defer watchdog()
	turnRequest := make(chan jsonrpc.Request, 1)
	interrupt := make(chan jsonrpc.Request, 1)
	acknowledge := make(chan struct{})
	startCount := 0
	openCount := 0
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			openCount++
			if openCount == 2 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"S"}}`)}
			}
			fallthrough
		case "thread/resume":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"idle"}}}`)}
		case "turn/start":
			startCount++
			switch startCount {
			case 2:
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"sibling"}}`)}
			case 3:
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
			}
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
	if runErr := awaitExecutionTest(t, ctx, done); !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", runErr)
	}
	close(acknowledge)
	req := awaitExecutionTest(t, ctx, interrupt)
	var ids map[string]string
	if decodeErr := json.Unmarshal(req.Params, &ids); decodeErr != nil || ids["threadId"] != "T" || ids["turnId"] != "U" {
		t.Fatalf("interrupt params = %s, decode error = %v", req.Params, decodeErr)
	}

	// An accepted interrupt is not the terminal. A duplicate handle cannot
	// submit while the original reservation is still active.
	wait, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, runErr := duplicate.Run(wait, "must stay queued", nil); !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("duplicate Run error = %v, want deadline", runErr)
	}
	select {
	case req := <-turnRequest:
		t.Fatalf("duplicate handle sent work before terminal: %s", req.Params)
	default:
	}
	// Sibling work is independent while the old interrupt has no terminal.
	sibling, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	siblingEvents, startErr := sibling.RunStreamed(ctx, "sibling work", nil)
	if startErr != nil {
		t.Fatal(startErr)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "S", "turnId": "sibling", "status": "completed"}))
	if _, ok := awaitExecutionTest(t, ctx, siblingEvents).(*types.TurnCompleted); !ok {
		t.Fatal("sibling did not complete during cancellation reconciliation")
	}

	ordered, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"T","status":{"type":"idle"}}}`))
	awaitExecutionTest(t, ctx, ordered)
	if _, runErr := duplicate.Run(ctx, "unconfirmed work", nil); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("admission after interrupt acknowledgement without terminal: %v", runErr)
	}
	reloaded, resumeErr := c.ResumeThread(ctx, "T", nil)
	if resumeErr != nil {
		t.Fatal(resumeErr)
	}
	if _, startErr := reloaded.RunStreamed(ctx, "idle snapshot is not SDK completion", nil); !errors.Is(startErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("idle resume incorrectly cleared a canceled SDK operation: %v", startErr)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "U", "status": "interrupted"}))
	awaitExecutionTest(t, ctx, ordered)
	next, err := duplicate.RunStreamed(ctx, "after the old terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "next", "status": "completed"}))
	terminal, ok := awaitExecutionTest(t, ctx, next).(*types.TurnCompleted)
	if !ok || terminal.TurnID != "next" {
		t.Fatalf("unexpected next terminal: %#v", terminal)
	}
	select {
	case late := <-interrupt:
		t.Fatalf("old cancellation interrupted a later operation: %s", late.Params)
	default:
	}
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
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"resumed","status":{"type":"idle"}}}`)}
		case "thread/resume":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"resumed","status":{"type":"active"}}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"new"}}`)}
		}
		t.Errorf("unexpected provider method %s", req.Method)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	original, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := c.ResumeThread(ctx, original.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "stale"}))
	awaitExecutionTest(t, ctx, observed)
	for _, handle := range []*Thread{original, thread} {
		if _, runErr := handle.RunStreamed(ctx, "would steer external work", nil); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
			t.Fatalf("stale terminal released unresolved external work: %v", runErr)
		}
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
	turns := make(chan string, 3)
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "thread/compact/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		case "turn/start":
			var params struct {
				Input []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil || len(params.Input) != 1 {
				t.Errorf("turn input = %s (%v)", req.Params, err)
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
			}
			id := params.Input[0].Text
			turns <- id
			payload, err := json.Marshal(map[string]any{"turn": map[string]string{"id": id}})
			if err != nil {
				t.Errorf("encode turn: %v", err)
			}
			return jsonrpc.Response{ID: req.ID, Result: payload}
		default:
			t.Errorf("unexpected provider method %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, prior := range []string{"older", "latest"} {
		out, startErr := thread.RunStreamed(ctx, prior, nil)
		if startErr != nil {
			t.Fatal(startErr)
		}
		awaitExecutionTest(t, ctx, turns)
		peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": prior, "status": "completed"}))
		for range out {
		}
	}
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := thread.Compact(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("thread/compacted", map[string]string{"threadId": "T", "turnId": "older"}))
	awaitExecutionTest(t, ctx, observed)
	if _, err := thread.RunStreamed(ctx, "silent compact has not finished", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("admission after silent compact = %v", err)
	}
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "compact"}))
	peer.push(json.RawMessage(`{"method":"item/completed","params":{"threadId":"T","turnId":"compact","item":{"type":"contextCompaction","id":"item"}}}`))
	if _, err := thread.RunStreamed(ctx, "compaction item is not a terminal", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("admission before compact terminal = %v", err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "compact", "status": "completed"}))
	for range 3 {
		awaitExecutionTest(t, ctx, observed)
	}
	peer.push(notify("thread/compacted", map[string]string{"threadId": "T", "turnId": "compact"}))
	if event, err := result.Wait(ctx); err != nil || event.TurnID != "compact" {
		t.Fatalf("late CompactResult.Wait = %+v, %v", event, err)
	}
	out, startErr := thread.RunStreamed(ctx, "next", nil)
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

func TestClient_ResumeRetainsTerminalObservedBeforeReply(t *testing.T) {
	t.Parallel()
	t.Run("matching terminal resolves fence", func(t *testing.T) {
		t.Parallel()
		testResumeBeforeReply(t, false)
	})
	t.Run("later active status keeps fence", func(t *testing.T) {
		t.Parallel()
		testResumeBeforeReply(t, true)
	})
}

func testResumeBeforeReply(t *testing.T, newerActive bool) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	resumeSent := make(chan struct{})
	releaseReply := make(chan struct{})
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/resume":
			close(resumeSent)
			select {
			case <-releaseReply:
			case <-ctx.Done():
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"resumed","status":{"type":"active"},"turns":[{"id":"old","status":"inProgress"}]}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
		}
		t.Errorf("unexpected provider method %s", req.Method)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	type resumeResult struct {
		thread *Thread
		err    error
	}
	resumed := make(chan resumeResult, 1)
	go func() {
		thread, resumeErr := c.ResumeThread(ctx, "resumed", nil)
		resumed <- resumeResult{thread: thread, err: resumeErr}
	}()
	awaitExecutionTest(t, ctx, resumeSent)
	peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"resumed","status":{"type":"active","activeFlags":[]}}}`))
	awaitExecutionTest(t, ctx, observed)
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "old", "status": "completed"}))
	awaitExecutionTest(t, ctx, observed)
	if newerActive {
		peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"resumed","status":{"type":"active","activeFlags":[]}}}`))
		awaitExecutionTest(t, ctx, observed)
	}
	close(releaseReply)
	loaded := awaitExecutionTest(t, ctx, resumed)
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	if newerActive {
		if _, startErr := loaded.thread.RunStreamed(ctx, "newer active work is unknown", nil); !errors.Is(startErr, ErrThreadExecutionUnconfirmed) {
			t.Fatalf("older terminal incorrectly cleared newer active status: %v", startErr)
		}
		peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"resumed","status":{"type":"idle"}}}`))
		awaitExecutionTest(t, ctx, observed)
	}
	out, startErr := loaded.thread.RunStreamed(ctx, "new work after terminal", nil)
	if startErr != nil {
		t.Fatal(startErr)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "resumed", "turnId": "next", "status": "completed"}))
	if _, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted); !ok {
		t.Fatal("new operation did not receive its terminal")
	}
}

func TestClient_CompletedCancellationCannotInterruptNextTurn(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	started := make(chan struct{})
	allowReply := make(chan struct{})
	startCount := 0
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "turn/start":
			startCount++
			if startCount == 2 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
			}
			close(started)
			select {
			case <-allowReply:
			case <-ctx.Done():
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"old"}}`)}
		}
		t.Errorf("unexpected request after cancellation race: %s %s", req.Method, req.Params)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	oldCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	canceled := make(chan error, 1)
	go func() {
		_, runErr := thread.Run(oldCtx, "old work", nil)
		canceled <- runErr
	}()
	awaitExecutionTest(t, ctx, started)
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "old"}))
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "old", "status": "completed"}))
	awaitExecutionTest(t, ctx, observed)
	awaitExecutionTest(t, ctx, observed)
	cancel()
	if runErr := awaitExecutionTest(t, ctx, canceled); !errors.Is(runErr, context.Canceled) {
		t.Fatalf("old run cancellation = %v", runErr)
	}
	close(allowReply)
	next, err := thread.RunStreamed(ctx, "next work", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "next", "status": "completed"}))
	if terminal, ok := awaitExecutionTest(t, ctx, next).(*types.TurnCompleted); !ok || terminal.TurnID != "next" {
		t.Fatalf("new operation terminal = %#v", terminal)
	}
}

func TestClient_ExternalActiveStatusFencesAdmissionBeforeTurnStart(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method != "thread/start" {
			t.Errorf("active thread received work before ownership resolved: %s", req.Method)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"T","status":{"type":"active","activeFlags":[]}}}`))
	awaitExecutionTest(t, ctx, ordered)
	if _, runErr := thread.RunStreamed(ctx, "would implicitly steer", nil); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("new work admitted before external root start: %v", runErr)
	}
}

func TestClient_MatchingLegacyCompactionCompletesAdmission(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "thread/compact/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
		}
		t.Errorf("unexpected method %s", req.Method)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := thread.Compact(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "compact"}))
	peer.push(notify("thread/compacted", map[string]string{"threadId": "T", "turnId": "compact"}))
	if compact, waitErr := result.Wait(ctx); waitErr != nil || compact.TurnID != "compact" {
		t.Fatalf("legacy compaction = %#v, %v", compact, waitErr)
	}
	out, err := thread.RunStreamed(ctx, "new work after legacy completion", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "next", "status": "completed"}))
	if _, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted); !ok {
		t.Fatal("next terminal missing")
	}
}

func TestClient_FreshIdleResumeOnlyClearsUnchangedExternalFence(t *testing.T) {
	t.Parallel()
	t.Run("unchanged external fence", func(t *testing.T) {
		t.Parallel()
		testFreshIdleResume(t, false, "")
	})
	t.Run("new activity during resume", func(t *testing.T) {
		t.Parallel()
		testFreshIdleResume(t, true, "")
	})
	t.Run("idle reply without explicit thread ID", func(t *testing.T) {
		t.Parallel()
		testFreshIdleResume(t, false, `{"thread":{"status":{"type":"idle"}}}`)
	})
	t.Run("idle reply with conflicting thread IDs", func(t *testing.T) {
		t.Parallel()
		testFreshIdleResume(t, false, `{"threadId":"T","thread":{"id":"other","status":{"type":"idle"}}}`)
	})
}

func testFreshIdleResume(t *testing.T, newerActive bool, invalidIdleReply string) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	secondResume := make(chan struct{})
	allowReply := make(chan struct{})
	resumeCount := 0
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/resume":
			resumeCount++
			if resumeCount == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"active"}}}`)}
			}
			close(secondResume)
			select {
			case <-allowReply:
			case <-ctx.Done():
			}
			if invalidIdleReply != "" {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(invalidIdleReply)}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"idle"}}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next"}}`)}
		}
		t.Errorf("unexpected provider method %s", req.Method)
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	first, err := c.ResumeThread(ctx, "T", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, startErr := first.RunStreamed(ctx, "unknown active work", nil); !errors.Is(startErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("missing initial external fence: %v", startErr)
	}
	ordered, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}

	reloaded := make(chan error, 1)
	go func() {
		_, resumeErr := c.ResumeThread(ctx, "T", nil)
		reloaded <- resumeErr
	}()
	awaitExecutionTest(t, ctx, secondResume)
	if newerActive {
		peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "external"}))
		awaitExecutionTest(t, ctx, ordered)
	}
	close(allowReply)
	if resumeErr := awaitExecutionTest(t, ctx, reloaded); resumeErr != nil {
		t.Fatal(resumeErr)
	}

	if newerActive || invalidIdleReply != "" {
		if _, startErr := first.RunStreamed(ctx, "new activity is unresolved", nil); !errors.Is(startErr, ErrThreadExecutionUnconfirmed) {
			t.Fatalf("idle reply incorrectly cleared the external fence: %v", startErr)
		}
		return
	}
	out, err := first.RunStreamed(ctx, "external fence recovered", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.push(notify("turn/completed", map[string]string{"threadId": "T", "turnId": "next", "status": "completed"}))
	if terminal, ok := awaitExecutionTest(t, ctx, out).(*types.TurnCompleted); !ok || terminal.TurnID != "next" {
		t.Fatalf("new operation terminal = %#v", terminal)
	}
}

func TestClient_IdleResumeCannotOvertakeQueuedProviderStart(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	secondResume := make(chan struct{})
	allowIdle := make(chan struct{})
	resumeCount := 0
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"blocker"}}`)}
		case "thread/resume":
			resumeCount++
			if resumeCount == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"active"}}}`)}
			}
			close(secondResume)
			select {
			case <-allowIdle:
			case <-ctx.Done():
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"idle"}}}`)}
		case "wire/barrier":
		default:
			t.Errorf("unexpected request while provider activity is queued: %s", req.Method)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	thread, err := c.ResumeThread(ctx, "T", nil)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	resumeCtx, cancelResume := context.WithCancel(ctx)
	defer cancelResume()
	result := make(chan error, 1)
	go func() {
		_, resumeErr := c.ResumeThread(resumeCtx, thread.ID(), nil)
		result <- resumeErr
	}()
	awaitExecutionTest(t, ctx, secondResume)

	// The first notification holds the dispatcher on another thread.
	// Queue the foreign root before letting the peer write the idle reply.
	release := holdExecutionGuard(t, ctx, blocker)
	defer release()
	peer.push(notify("turn/started", map[string]string{"threadId": "blocker", "turnId": "barrier"}))
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "external"}))
	close(allowIdle)

	// A distinct, ordinary reply proves the reader is past the idle
	// response although the dispatcher still cannot apply the foreign root.
	if _, sendErr := c.demux.Send(ctx, "wire/barrier", nil); sendErr != nil {
		t.Fatal(sendErr)
	}
	cancelResume()
	if resumeErr := awaitExecutionTest(t, ctx, result); !errors.Is(resumeErr, context.Canceled) {
		t.Fatalf("idle resume bypassed the blocked dispatcher: %v", resumeErr)
	}
	if _, startErr := thread.RunStreamed(ctx, "must not steer external work", nil); !errors.Is(startErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("queued external start did not preserve the fence: %v", startErr)
	}
}
