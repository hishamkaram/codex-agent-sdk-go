package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestFileApprovalResolvesStartedItem(t *testing.T) {
	t.Parallel()
	var captured types.ApprovalRequest
	opts := types.NewCodexOptions()
	opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
		captured = request
		return types.ApprovalDeny{}
	}
	c, _ := setupMockClient(t, opts, nil)
	c.handleNotification(jsonrpc.Notification{Method: "item/started", Params: json.RawMessage(
		`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"fileChange","id":"item-1","changes":[{"path":"one.txt","kind":{"type":"add"},"diff":"+one"},{"path":"two.txt","kind":{"type":"update","move_path":"three.txt"},"diff":"+two"}]}}`,
	)})
	c.handleServerRequest(t.Context(), c.demux, jsonrpc.ServerRequest{ID: 42, Method: "item/fileChange/requestApproval", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`)})
	data, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Changes []types.FileChangePart `json:"changes"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 2 || result.Changes[0].Path != "one.txt" || result.Changes[1].Path != "two.txt" {
		t.Fatalf("approval lost file context: %s", data)
	}
}
