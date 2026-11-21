package threadexec

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestStreamFiltersForeignAuxiliaryEvents(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	record := NewRecord(false)
	t.Cleanup(record.Close)
	op, err := record.admit(ctx, "thread", Turn, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire := Wire{PendingActivity: func() (bool, error) { return false, nil }}
	if err := record.markDispatch(ctx, op, wire); err != nil {
		t.Fatal(err)
	}
	record.acknowledge(op, json.RawMessage(`{"turn":{"id":"current"}}`))

	eventPairs := []struct {
		foreign types.ThreadEvent
		current types.ThreadEvent
	}{
		{&types.ItemGuardianApprovalReviewStarted{TurnID: "foreign"}, &types.ItemGuardianApprovalReviewStarted{TurnID: "current"}},
		{&types.ItemGuardianApprovalReviewCompleted{TurnID: "foreign"}, &types.ItemGuardianApprovalReviewCompleted{TurnID: "current"}},
		{&types.FileChangePatchUpdated{TurnID: "foreign"}, &types.FileChangePatchUpdated{TurnID: "current"}},
		{&types.ModelRerouted{TurnID: "foreign"}, &types.ModelRerouted{TurnID: "current"}},
		{&types.ModelVerification{TurnID: "foreign"}, &types.ModelVerification{TurnID: "current"}},
		{&types.ModelSafetyBufferingUpdated{TurnID: "foreign"}, &types.ModelSafetyBufferingUpdated{TurnID: "current"}},
		{&types.ErrorEvent{TurnID: "foreign"}, &types.ErrorEvent{TurnID: "current"}},
		{&types.UnknownEvent{TurnID: "foreign", Method: "future/event"}, &types.UnknownEvent{TurnID: "current", Method: "future/event"}},
	}
	want := make([]types.ThreadEvent, 0, len(eventPairs)+1)
	for _, events := range eventPairs {
		record.Observe(events.foreign)
		record.Observe(events.current)
		want = append(want, events.current)
	}
	threadEvent := &types.ThreadGoalUpdated{ThreadID: "thread"}
	terminal := &types.TurnCompleted{ThreadID: "thread", TurnID: "current", Status: "completed"}
	record.Observe(threadEvent)
	record.Observe(terminal)
	want = append(want, threadEvent)

	var got []types.ThreadEvent
	var result *types.TurnCompleted
	for event := range record.Stream(ctx, op, wire) {
		if result != nil {
			t.Fatalf("additional event after terminal: %#v", event)
		}
		if completed, ok := event.(*types.TurnCompleted); ok {
			result = completed
		} else {
			got = append(got, event)
		}
	}
	if result == nil || result.ThreadID != "thread" || result.TurnID != "current" || result.Status != "completed" {
		t.Fatalf("terminal = %+v, want successful current turn", result)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stream forwarded %d events, want %d matches in order: got %v", len(got), len(want), got)
	}
}
