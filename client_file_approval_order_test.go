package codex

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestFileApprovalOrderedDispatch(t *testing.T) {
	t.Parallel()
	started := `{"method":"item/started","params":{"threadId":"thread","turnId":"turn","item":{"type":"fileChange","id":"item","changes":[{"path":"proof.txt","kind":{"type":"add"}}]}}}`
	for _, tc := range []struct {
		name, before, turn string
		wantCalls          int
	}{
		{"ordered", started, "turn", 1},
		{"missing", "", "turn", 0},
		{"wrong turn", started, "other", 0},
		{"completed", started + "\n" + `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn","status":"completed"}}}`, "turn", 0},
		{"decode gap", started + "\n" + `{broken`, "turn", 0},
		{"missing thread identity", started + "\n" + `{"method":"turn/completed","params":{"turn":{"id":"turn","status":"completed"}}}`, "turn", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request := `{"id":42,"method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"` + tc.turn + `","itemId":"item"}}`
			var calls atomic.Int32
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls.Add(1)
				file := request.(*types.FileChangeApprovalRequest)
				if len(file.Changes) != 1 || file.Changes[0].Path != "proof.txt" {
					t.Errorf("wrong context: %+v", file)
				}
				return types.ApprovalAccept{}
			}
			_, server := setupMockClient(t, opts, nil)
			if _, err := server.serverOut.Write([]byte(tc.before + "\n" + request + "\n")); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Result struct {
					Decision string `json:"decision"`
				} `json:"result"`
			}
			select {
			case raw := <-server.approvalReplies:
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("server never received its reply")
			}
			if calls.Load() != int32(tc.wantCalls) {
				t.Fatalf("callback calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
			want := "decline"
			if tc.wantCalls == 1 {
				want = "accept"
			}
			if response.Result.Decision != want {
				t.Fatalf("decision = %q, want %q", response.Result.Decision, want)
			}
		})
	}
}
