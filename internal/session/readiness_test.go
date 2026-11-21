package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSourceLossSerializesWithReadyEnrollment(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	defer stopDispatch()

	s := New(types.NewCodexOptions(), nil, nil)
	startLoss := make(chan struct{})
	attemptingLoss := make(chan struct{})
	type outcome struct {
		lost     bool
		enrolled bool
	}
	result := make(chan outcome, 1)
	var enrolled atomic.Bool
	dispatch := func(ctx context.Context, _ *jsonrpc.Demux, done chan struct{}) {
		defer close(done)
		select {
		case <-startLoss:
		case <-ctx.Done():
			return
		}
		close(attemptingLoss)
		lost := s.MarkSourceLost()
		result <- outcome{lost: lost, enrolled: enrolled.Load()}
	}
	if err := s.StartDispatcher(dispatchCtx, stopDispatch, nil, dispatch); err != nil {
		t.Fatalf("start dispatcher: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := s.Close(closeCtx); err != nil {
			t.Errorf("close session: %v", err)
		}
	})

	if err := s.WithReady(func() {
		// The source-loss transition shares this lock with admission. Starting
		// the transition here exercises the interval before insertion.
		if s.lifecycleMu.TryLock() {
			s.lifecycleMu.Unlock()
			t.Error("enrollment ran outside the lifecycle lock")
		}
		close(startLoss)
		select {
		case <-attemptingLoss:
		case <-ctx.Done():
			t.Fatal("dispatcher did not reach the source-loss transition")
		}
		enrolled.Store(true)
	}); err != nil {
		t.Fatalf("enroll while ready: %v", err)
	}

	select {
	case got := <-result:
		if !got.lost || !got.enrolled {
			t.Fatalf("source loss = %+v, want transition after enrollment", got)
		}
	case <-ctx.Done():
		t.Fatal("source loss did not finish after enrollment")
	}
	if err := s.WithReady(func() { t.Error("enrolled after source loss") }); !errors.Is(err, types.ErrClientNotConnected) {
		t.Fatalf("enroll after source loss: %v, want ErrClientNotConnected", err)
	}
}
