package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
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
			calls := 0
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls++
				data, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if string(fields["grantRoot"]) != tc.root {
					t.Fatalf("grantRoot was not preserved: %s", data)
				}
				return types.ApprovalDeny{}
			}
			c, _ := setupMockClient(t, opts, nil)
			if tc.files {
				startFileApprovalForUpdate(c)
			}
			if tc.invalidated {
				c.handleNotification(jsonrpc.Notification{
					Method: "item/fileChange/patchUpdated",
					Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","changes":{}}`),
				})
			}
			c.handleServerRequest(t.Context(), c.demux, jsonrpc.ServerRequest{
				ID: 42, Method: "item/fileChange/requestApproval",
				Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","grantRoot":` + tc.root + `}`),
			})
			if calls != tc.wantCalls {
				t.Fatalf("callback calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}
