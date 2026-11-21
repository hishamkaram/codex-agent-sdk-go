package threadexec

import (
	"context"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func (t *Record) Stream(ctx context.Context, op *Operation, wire Wire) <-chan types.ThreadEvent {
	out := make(chan types.ThreadEvent, InboxBuffer)
	go t.streamLoop(ctx, op, out, wire)
	return out
}

func (t *Record) streamLoop(ctx context.Context, op *Operation, out chan<- types.ThreadEvent, wire Wire) {
	defer close(out)
	defer t.consumeDone(op)
	expected := op.ackID
	for {
		select {
		case <-ctx.Done():
			t.startExecutionCleanup(ctx, op, nil, wire)
			return
		case <-t.done:
			return
		case ev, ok := <-op.events:
			if !ok {
				t.forwardStreamEvent(ctx, out, op.terminal)
				return
			}
			if expected == "" {
				if started, ok := ev.(*types.TurnStarted); ok && IsRootTurn(started.TurnID, started.RootTurnID) {
					expected = started.TurnID
				}
			}
			id := turnIDFromStreamEvent(ev)
			if (expected == "" || id == "" || id == expected) && !t.forwardStreamEvent(ctx, out, ev) {
				t.startExecutionCleanup(ctx, op, nil, wire)
				return
			}
		}
	}
}

func (t *Record) forwardStreamEvent(ctx context.Context, out chan<- types.ThreadEvent, ev types.ThreadEvent) bool {
	if ev == nil {
		return true
	}
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
	case <-t.done:
	}
	return false
}

func turnIDFromStreamEvent(ev types.ThreadEvent) string {
	switch event := ev.(type) {
	case *types.TurnStarted:
		return event.TurnID
	case *types.ItemStarted:
		return event.TurnID
	case *types.ItemUpdated:
		return event.TurnID
	case *types.ItemCompleted:
		return event.TurnID
	case *types.TokenUsageUpdated:
		return event.TurnID
	case *types.TurnDiffUpdated:
		return event.TurnID
	case *types.TurnPlanUpdated:
		return event.TurnID
	case *types.TurnModerationMetadata:
		return event.TurnID
	case *types.ContextCompacted:
		return event.TurnID
	default:
		return turnIDFromAuxiliaryEvent(ev)
	}
}

func turnIDFromAuxiliaryEvent(ev types.ThreadEvent) string {
	switch event := ev.(type) {
	case *types.ItemGuardianApprovalReviewStarted:
		return event.TurnID
	case *types.ItemGuardianApprovalReviewCompleted:
		return event.TurnID
	case *types.FileChangePatchUpdated:
		return event.TurnID
	case *types.ModelRerouted:
		return event.TurnID
	case *types.ModelVerification:
		return event.TurnID
	case *types.ModelSafetyBufferingUpdated:
		return event.TurnID
	case *types.ErrorEvent:
		return event.TurnID
	case *types.UnknownEvent:
		return event.TurnID
	default:
		return ""
	}
}
