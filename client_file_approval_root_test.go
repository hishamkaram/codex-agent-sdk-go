package codex

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestFileApprovalExposesGrantRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, root  string
		files       bool
		invalidated bool
		wantCalls   int
	}{
		{"root only", `"/project"`, false, false, 1},
		{"root and files", `"/project"`, true, false, 1},
		{"blank root", `"   "`, true, false, 0},
		{"null root without files", `null`, false, false, 0},
		{"invalidated files cannot become root only", `"/project"`, true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls.Add(1)
				data, err := json.Marshal(request)
				if err != nil {
					t.Error(err)
					return types.ApprovalDeny{}
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Error(err)
					return types.ApprovalDeny{}
				}
				if string(fields["grantRoot"]) != tc.root {
					t.Errorf("grantRoot was not preserved: %s", data)
				}
				return types.ApprovalDeny{}
			}
			_, server := setupMockClient(t, opts, nil)
			if tc.files {
				startFileApprovalForUpdate(server)
			}
			if tc.invalidated {
				server.push(json.RawMessage(`{"method":"item/fileChange/patchUpdated","params":{"threadId":"thread","turnId":"turn","itemId":"item","changes":{}}}`))
			}
			server.push(json.RawMessage(`{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"turn","itemId":"item","grantRoot":` + tc.root + `}}`))
			wantReply := `{"decision":"decline"}`
			if tc.wantCalls == 0 {
				wantReply = `{"decision":"decline","reason":"no approval callback configured"}`
			}
			assertApprovalReply(t, server, "42", wantReply)
			if got := int(calls.Load()); got != tc.wantCalls {
				t.Fatalf("callback calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

func startFileApprovalForUpdate(server *mockCodexServer) {
	server.push(json.RawMessage(`{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[{"path":"old.txt","kind":{"type":"add"},"diff":"+old"}]}}}`))
}
