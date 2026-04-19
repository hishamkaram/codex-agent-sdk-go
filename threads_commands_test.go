package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Unit tests for the v0.4.0 thread mutating methods. Happy paths are
// exercised by tests/integration_commands_test.go (real codex). Here
// we cover only the input-validation + closed-thread guards which
// don't need the wire.

func TestThreadCommands_InputValidation(t *testing.T) {
	t.Parallel()
	// Use a closed Thread so the methods short-circuit without
	// reaching the demux. The closed-check fires after the
	// input-validation-check for every method, so to test the input
	// validation we use a fresh (uninitialized) Thread.
	tests := []struct {
		name    string
		call    func(*Thread) error
		wantErr string
	}{
		{
			"Rollback negative",
			func(th *Thread) error { return th.Rollback(context.Background(), -1) },
			"numTurns must be >= 1",
		},
		{
			"Rollback zero",
			func(th *Thread) error { return th.Rollback(context.Background(), 0) },
			"numTurns must be >= 1",
		},
		{
			"SetName empty",
			func(th *Thread) error { return th.SetName(context.Background(), "") },
			"name must not be empty",
		},
		{
			"Steer empty",
			func(th *Thread) error { return th.Steer(context.Background(), "") },
			"text must not be empty",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th := &Thread{id: "test"}
			th.activeTurnID.Store("")
			err := tt.call(th)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestThread_Steer_NoActiveTurn(t *testing.T) {
	t.Parallel()
	// Thread with empty activeTurnID and a non-empty input should
	// return "no active turn" — same precondition as Interrupt.
	th := &Thread{id: "test"}
	th.activeTurnID.Store("")
	err := th.Steer(context.Background(), "extend the plan")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no active turn") {
		t.Errorf("err = %q, want 'no active turn'", err)
	}
}

func TestThread_Mutating_ClosedThread(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(*Thread) error
	}{
		{"Rollback", func(th *Thread) error { return th.Rollback(context.Background(), 1) }},
		{"SetName", func(th *Thread) error { return th.SetName(context.Background(), "new") }},
		{"Steer", func(th *Thread) error { return th.Steer(context.Background(), "x") }},
		{"CleanBackgroundTerminals", func(th *Thread) error { return th.CleanBackgroundTerminals(context.Background()) }},
		{"Compact", func(th *Thread) error {
			_, err := th.Compact(context.Background(), nil)
			return err
		}},
		{"Summarize", func(th *Thread) error {
			_, err := th.Summarize(context.Background())
			return err
		}},
		{"StartReview", func(th *Thread) error {
			_, err := th.StartReview(context.Background(), reviewOptsForTest())
			return err
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th := &Thread{id: "test"}
			th.activeTurnID.Store("turn-1") // bypass Steer's "no active turn" check
			th.markClosed()
			err := tt.call(th)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "thread closed") {
				t.Errorf("err = %q, want 'thread closed'", err)
			}
		})
	}
}

func TestThread_StartReview_TargetRequired(t *testing.T) {
	t.Parallel()
	// Target zero-value → reject before reaching transport.
	th := &Thread{id: "test"}
	_, err := th.StartReview(context.Background(), types.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "opts.Target.Type is required") {
		t.Errorf("err = %q", err)
	}
}

func TestCompactResult_NilSafe(t *testing.T) {
	t.Parallel()
	var r *CompactResult
	_, err := r.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on nil CompactResult")
	}
}

func TestCompactResult_WaitCtxCancel(t *testing.T) {
	t.Parallel()
	// Simulate a CompactResult whose subscription channel will
	// never fire. Wait must honor ctx cancellation promptly.
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	th := &Thread{id: "test"}
	th.compactSub.Store(chPtr)
	r := &CompactResult{ThreadID: "test", thread: th, chPtr: chPtr}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately
	_, err := r.Wait(ctx)
	if err == nil {
		t.Fatal("expected ctx error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("err = %q", err)
	}
	// After Wait with canceled ctx, detach must have cleared the
	// subscription so a subsequent Compact could install its own.
	if th.compactSub.Load() != nil {
		t.Error("detach did not clear subscription on ctx-cancel")
	}
}

func TestCompactResult_WaitClosedThread(t *testing.T) {
	t.Parallel()
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	th := &Thread{id: "test"}
	th.compactSub.Store(chPtr)
	r := &CompactResult{ThreadID: "test", thread: th, chPtr: chPtr}

	// markClosed closes the channel — Wait observes it.
	th.markClosed()
	_, err := r.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on closed thread")
	}
	if !strings.Contains(err.Error(), "thread closed before compact completed") {
		t.Errorf("err = %q", err)
	}
}

// reviewOptsForTest builds a minimal-valid ReviewOptions for
// closed-thread tests (no live RPC, so Target contents don't matter
// beyond non-empty Type).
func reviewOptsForTest() types.ReviewOptions {
	return types.ReviewOptions{Target: types.ReviewTargetUncommittedChanges()}
}
