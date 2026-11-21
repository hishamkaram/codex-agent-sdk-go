package threadexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestCompactResult_WaitCtxCancel(t *testing.T) {
	t.Parallel()
	// Simulate a CompactResult whose subscription channel will
	// never fire. Wait must honor ctx cancellation promptly.
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	record := NewRecord(false)
	record.compactSub.Store(chPtr)
	r := CompactWaiter{record: record, channel: chPtr}

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
	if record.compactSub.Load() != nil {
		t.Error("detach did not clear subscription on ctx-cancel")
	}
}

func TestCompactResult_WaitSuccessDetaches(t *testing.T) {
	t.Parallel()
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	record := NewRecord(false)
	record.compactSub.Store(chPtr)
	r := CompactWaiter{record: record, channel: chPtr}
	want := &types.ContextCompacted{ThreadID: "test"}
	ch <- want

	got, err := r.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got != want {
		t.Fatalf("Wait event = %p, want %p", got, want)
	}
	if record.compactSub.Load() != nil {
		t.Fatal("successful Wait did not detach subscription")
	}
}

func TestCompactResult_WaitClosedThread(t *testing.T) {
	t.Parallel()
	ch := make(chan *types.ContextCompacted, 1)
	chPtr := &ch
	record := NewRecord(false)
	record.compactSub.Store(chPtr)
	r := CompactWaiter{record: record, channel: chPtr}

	// markClosed closes the channel — Wait observes it.
	record.Close()
	_, err := r.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on closed thread")
	}
	if !strings.Contains(err.Error(), "thread closed before compact completed") {
		t.Errorf("err = %q", err)
	}
}

func TestCompactAdmissionRejectsAnUndrainedResult(t *testing.T) {
	t.Parallel()

	record := NewRecord(false)
	previous := make(chan *types.ContextCompacted, 1)
	record.compactSub.Store(&previous)

	op, err := record.admit(t.Context(), "thread", Compact, false, nil)
	if op != nil || !errors.Is(err, types.ErrCompactResultPending) {
		t.Fatalf("admit = (%v, %v), want no operation and ErrCompactResultPending", op, err)
	}
	if record.compactSub.Load() != &previous {
		t.Fatal("rejected admission displaced the pending result")
	}
}
