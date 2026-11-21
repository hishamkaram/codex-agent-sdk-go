package approval

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/events"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestFileApprovalUsesLatestPatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, changes string
		valid         bool
		emptyStart    bool
	}{
		{"replacement", `[{"path":"new.txt","kind":{"type":"update","move_path":"renamed.txt"},"diff":"+new"}]`, true, false},
		{"initially empty", `[{"path":"new.txt","kind":{"type":"update","move_path":"renamed.txt"},"diff":"+new"}]`, true, true},
		{"empty", `[]`, false, false},
		{"null", `null`, false, false},
		{"invalid shape", `{}`, false, false},
		{"invalid operation", `[{"path":"new.txt","kind":{"type":"unknown"}}]`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var owner Owner
			startFileApprovalForUpdate(t, &owner)
			if tc.emptyStart {
				observeNotification(t, &owner, jsonrpc.Notification{
					Method: "item/started",
					Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[]}}`),
				})
			}
			observeNotification(t, &owner, jsonrpc.Notification{
				Method: "item/fileChange/patchUpdated",
				Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","changes":` + tc.changes + `}`),
			})
			request := &types.FileChangeApprovalRequest{
				ThreadID: "thread", TurnID: "turn", ItemID: "item",
				Path: "inline.txt", Operation: "create",
			}
			if valid := owner.resolveFileApproval(request); valid != tc.valid {
				t.Fatalf("resolved = %v, want %v; context = %+v", valid, tc.valid, request.Changes)
			}
			if tc.valid {
				if len(request.Changes) != 1 || request.Path != "new.txt" || request.Diff != "+new" {
					t.Fatalf("stale context: %+v", request)
				}
				kind := request.Changes[0].Kind
				if kind == nil || kind.MovePath == nil || *kind.MovePath != "renamed.txt" {
					t.Fatalf("rename lost: %+v", kind)
				}
			}
		})
	}
}

func TestFileApprovalPatchDoesNotReviveCompletedItem(t *testing.T) {
	t.Parallel()
	var owner Owner
	startFileApprovalForUpdate(t, &owner)
	owner.Observe(&types.ItemCompleted{ThreadID: "thread", TurnID: "turn", ItemID: "item"})
	observeNotification(t, &owner, jsonrpc.Notification{
		Method: "item/fileChange/patchUpdated",
		Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","changes":[{"path":"late.txt","kind":{"type":"add"}}]}`),
	})
	if owner.resolveFileApproval(&types.FileChangeApprovalRequest{ThreadID: "thread", TurnID: "turn", ItemID: "item"}) {
		t.Fatal("late patch revived completed approval context")
	}
}

func startFileApprovalForUpdate(t *testing.T, owner *Owner) {
	t.Helper()
	observeNotification(t, owner, jsonrpc.Notification{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[{"path":"old.txt","kind":{"type":"add"},"diff":"+old"}]}}`),
	})
}

func observeNotification(t *testing.T, owner *Owner, notification jsonrpc.Notification) {
	t.Helper()
	event, err := events.ParseEvent(notification)
	if err != nil {
		t.Fatal(err)
	}
	owner.Observe(event)
}
