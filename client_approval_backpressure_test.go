package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ApprovalOverflowPreservesOrderedDelivery(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	entered := make(chan struct{})
	opts := types.NewCodexOptions().WithApprovalCallback(func(ctx context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		close(entered)
		<-ctx.Done()
		return types.ApprovalDeny{}
	})
	peer := newMockCodexServer()
	writer := newBlockingFrameWriter()
	logger := sdklog.NewLoggerFromZap(nil)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(peer.serverIn), jsonrpc.NewLineWriter(writer), logger, jsonrpc.WithOrderedServerMessages())
	client := &Client{opts: opts, logger: logger, demux: demux}
	client.connected.Store(true)
	client.dispatcherCancel = stop
	client.dispatcherDone = make(chan struct{})
	demux.Run(ctx)
	go client.dispatch(ctx, demux, client.dispatcherDone)
	defer func() {
		_ = client.Close(ctx)
		peer.close()
	}()
	ordered, err := client.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	send := func(id int) {
		peer.push(json.RawMessage(fmt.Sprintf(`{"id":%d,"method":"item/permissions/requestApproval","params":{"threadId":"held","turnId":"hold","itemId":"item","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`, id)))
	}
	send(1)
	awaitExecutionTest(t, ctx, entered)
	for id := 2; id <= approvalQueueSize+2; id++ {
		send(id)
	}
	// The alternate denial lane has hit a blocked writer. The ordered
	// dispatcher must still reach an unrelated thread's terminal.
	awaitExecutionTest(t, ctx, writer.writeEntered)
	peer.push(notify("turn/completed", map[string]string{"threadId": "sibling", "turnId": "other", "status": "completed"}))
	event := awaitExecutionTest(t, ctx, ordered)
	terminal, ok := event.Event.(*types.TurnCompleted)
	if !ok || terminal.TurnID != "other" || event.Err != nil {
		t.Fatalf("sibling terminal = %#v", event)
	}
}
