package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
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
			var output bytes.Buffer
			demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(strings.NewReader(tc.before+"\n"+request+"\n")), jsonrpc.NewLineWriter(&output), nil, jsonrpc.WithOrderedServerMessages())
			defer demux.Close()
			demux.Run(ctx)
			select {
			case <-demux.LoopError():
			case <-ctx.Done():
				t.Fatal("demux did not finish buffering")
			}
			calls := 0
			opts := types.NewCodexOptions()
			opts.ApprovalCallback = func(_ context.Context, request types.ApprovalRequest) types.ApprovalDecision {
				calls++
				file := request.(*types.FileChangeApprovalRequest)
				if len(file.Changes) != 1 || file.Changes[0].Path != "proof.txt" {
					t.Errorf("wrong context: %+v", file)
				}
				return types.ApprovalAccept{}
			}
			client := &Client{opts: opts, logger: sdklog.NewLoggerFromZap(nil)}
			client.dispatch(ctx, demux, make(chan struct{}))
			if calls != tc.wantCalls {
				t.Fatalf("callback calls = %d, want %d", calls, tc.wantCalls)
			}
			var response struct {
				Result struct {
					Decision string `json:"decision"`
				} `json:"result"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
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
