package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_ApprovalBackpressureClosesUndeliverableReply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		overflow bool
	}{
		{name: "callback"},
		{name: "overflow", overflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testRequiredApprovalReply(t, tc.overflow)
		})
	}
}

func testRequiredApprovalReply(t *testing.T, overflow bool) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 9*time.Second)
	defer stop()
	entered := make(chan struct{})
	opts := types.NewCodexOptions().WithApprovalCallback(func(ctx context.Context, _ types.ApprovalRequest) types.ApprovalDecision {
		close(entered)
		if overflow {
			<-ctx.Done()
		}
		return types.ApprovalDeny{}
	})
	peer := newMockCodexServer()
	writer := newBlockingFrameWriter()
	logger := sdklog.NewLoggerFromZap(nil)
	closed := make(chan error, 1)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(peer.serverIn), jsonrpc.NewLineWriter(writer), logger,
		jsonrpc.WithOrderedServerMessages(),
		jsonrpc.WithUnrecoverableHandler(func(err error) { closed <- err }))
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
	held := make(chan error, 1)
	go func() {
		_, holdErr := demux.Send(ctx, "hold/writer", nil)
		held <- holdErr
	}()
	awaitExecutionTest(t, ctx, writer.writeEntered)
	send := func(id int) {
		peer.push(json.RawMessage(fmt.Sprintf(`{"id":%d,"method":"item/permissions/requestApproval","params":{"threadId":"held","turnId":"hold","itemId":"item","cwd":"/work","startedAtMs":1782904297563,"permissions":{"network":{"enabled":true}}}}`, id)))
	}
	send(1)
	awaitExecutionTest(t, ctx, entered)
	if overflow {
		for id := 2; id <= approvalQueueSize+2; id++ {
			send(id)
		}
	}
	// Both the callback and overflow paths can queue a reply behind a
	// held writer slot without blocking a sibling's ordered terminal.
	peer.push(notify("turn/completed", map[string]string{"threadId": "sibling", "turnId": "other", "status": "completed"}))
	event := awaitExecutionTest(t, ctx, ordered)
	terminal, ok := event.Event.(*types.TurnCompleted)
	if !ok || terminal.TurnID != "other" || event.Err != nil {
		t.Fatalf("sibling terminal = %#v", event)
	}
	failure := awaitExecutionTest(t, ctx, closed)
	if !errors.Is(failure, context.DeadlineExceeded) && !errors.Is(failure, jsonrpc.ErrFramePoisoned) {
		t.Fatalf("connection fault = %v", failure)
	}
	awaitExecutionTest(t, ctx, held)
	if _, sendErr := demux.Send(ctx, "after/undeliverable", nil); !errors.Is(sendErr, jsonrpc.ErrClosed) {
		t.Fatalf("connection survived an undelivered required reply: %v", sendErr)
	}
}
