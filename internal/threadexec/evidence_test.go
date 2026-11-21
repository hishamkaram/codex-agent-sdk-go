package threadexec

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestOperationRequiresMatchingTerminalEvidence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		reply     json.RawMessage
		event     types.ThreadEvent
		completed bool
	}{
		{"completed matching", json.RawMessage(`{"turn":{"id":"U1"}}`), &types.TurnCompleted{TurnID: "U1"}, true},
		{"completed mismatched", json.RawMessage(`{"turn":{"id":"U2"}}`), &types.TurnCompleted{TurnID: "U1"}, false},
		{"completed unknown", json.RawMessage(`{}`), &types.TurnCompleted{TurnID: "U1"}, false},
		{"failed matching", json.RawMessage(`{"turn":{"id":"U1"}}`), &types.TurnFailed{TurnID: "U1"}, true},
		{"failed unknown", json.RawMessage(`{}`), &types.TurnFailed{TurnID: "U1"}, false},
		{"non-terminal matching", json.RawMessage(`{"turn":{"id":"U1"}}`), &types.ItemStarted{TurnID: "U1"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := NewRecord(false)
			t.Cleanup(record.Close)
			op, err := record.admit(t.Context(), "T", Turn, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire := Wire{PendingActivity: func() (bool, error) { return false, nil }}
			if err := record.markDispatch(t.Context(), op, wire); err != nil {
				t.Fatal(err)
			}
			record.acknowledge(op, test.reply)
			record.Observe(test.event)
			if _, _, completed := record.Final(op); completed != test.completed {
				t.Fatalf("operation completed = %t, want %t", completed, test.completed)
			}
		})
	}
}
