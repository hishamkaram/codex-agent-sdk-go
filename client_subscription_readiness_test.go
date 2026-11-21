package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSubscribeThreadEventsRacingSourceLossGetsGapOrIsRejected(t *testing.T) {
	t.Parallel()

	client, peer := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type enrollment struct {
		events <-chan ThreadEventEnvelope
		err    error
	}
	start := make(chan struct{})
	subscribeReady := make(chan struct{})
	closeReady := make(chan struct{})
	peerClosed := make(chan struct{})
	result := make(chan enrollment, 1)
	go func() {
		close(subscribeReady)
		select {
		case <-start:
			events, err := client.SubscribeThreadEvents(ctx, 1)
			result <- enrollment{events: events, err: err}
		case <-ctx.Done():
		}
	}()
	go func() {
		defer close(peerClosed)
		close(closeReady)
		select {
		case <-start:
			peer.close()
		case <-ctx.Done():
		}
	}()

	awaitExecutionTest(t, ctx, subscribeReady)
	awaitExecutionTest(t, ctx, closeReady)
	close(start)
	got := awaitExecutionTest(t, ctx, result)
	awaitExecutionTest(t, ctx, peerClosed)
	awaitExecutionTest(t, ctx, client.session.DispatcherDone())

	if errors.Is(got.err, types.ErrClientNotConnected) && got.events == nil {
		return
	}
	if got.err != nil || got.events == nil {
		t.Fatalf("enroll during source loss: events=%v err=%v", got.events, got.err)
	}
	var gap *ThreadEventSourceGapError
	select {
	case event, ok := <-got.events:
		if !ok || event.Event != nil || event.ThreadID != "" || !errors.As(event.Err, &gap) || !errors.Is(event.Err, ErrThreadEventStreamGap) {
			t.Fatalf("admitted subscription received %+v (open=%v), want a terminal source gap", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("admitted subscription did not receive a source gap")
	}
	select {
	case _, ok := <-got.events:
		if ok {
			t.Fatal("subscription delivered an event after the source gap")
		}
	case <-ctx.Done():
		t.Fatal("subscription did not close after the source gap")
	}
}

func TestSubscribeThreadEventsCanResubscribeAfterRecoverableGap(t *testing.T) {
	t.Parallel()

	client, peer := setupMockClient(t, types.NewCodexOptions(), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := client.SubscribeThreadEvents(ctx, 1)
	if err != nil {
		t.Fatalf("subscribe before malformed frame: %v", err)
	}
	if _, writeErr := peer.serverOut.Write([]byte("not-json\n")); writeErr != nil {
		t.Fatalf("write malformed frame: %v", writeErr)
	}

	var gap *ThreadEventFrameGapError
	select {
	case event, ok := <-first:
		if !ok || !errors.As(event.Err, &gap) || gap.Cause == nil {
			t.Fatalf("first subscription received %+v (open=%v), want a frame gap with its cause", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("first subscription did not receive a frame gap")
	}
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("first subscription remained open after its gap")
		}
	case <-ctx.Done():
		t.Fatal("first subscription did not close after its gap")
	}

	second, err := client.SubscribeThreadEvents(ctx, 1)
	if err != nil {
		t.Fatalf("subscribe after recoverable gap: %v", err)
	}
	peer.push(notify("turn/started", map[string]any{
		"threadId": "resubscribed",
		"turn":     map[string]any{"id": "next-turn", "status": "inProgress", "items": []any{}},
	}))

	select {
	case event, ok := <-second:
		turn, isStart := event.Event.(*types.TurnStarted)
		if !ok || event.Err != nil || event.ThreadID != "resubscribed" || !isStart || turn.TurnID != "next-turn" {
			t.Fatalf("second subscription received %+v (open=%v), want the next turn start", event, ok)
		}
	case <-ctx.Done():
		t.Fatal("second subscription did not receive the next turn start")
	}
}
