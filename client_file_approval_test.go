package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestFileApprovalResolvesStartedItem(t *testing.T) {
	t.Parallel()
	captured := make(chan types.ApprovalRequest, 1)
	opts := types.NewCodexOptions()
	opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
		captured <- request
		return types.ApprovalDeny{}
	}
	_, server := setupMockClient(t, opts, nil)
	server.push(json.RawMessage(`{"method":"item/started","params":{"threadId":"thread-1","turnId":"turn-1","item":{"type":"fileChange","id":"item-1","changes":[{"path":"one.txt","kind":{"type":"add"},"diff":"+one"},{"path":"two.txt","kind":{"type":"update","move_path":"three.txt"},"diff":"+two"}]}}}`))
	server.push(json.RawMessage(`{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}}`))
	assertApprovalReply(t, server, "42", `{"decision":"decline"}`)
	var request types.ApprovalRequest
	select {
	case request = <-captured:
	default:
		t.Fatal("file approval callback was not called")
	}
	data, err := json.Marshal(request)
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
