package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type gatedRequestWriter struct {
	io.WriteCloser
	mu      sync.Mutex
	wait    <-chan struct{}
	entered chan<- struct{}
}

func (w *gatedRequestWriter) WriteContext(ctx context.Context, frame []byte) (int, error) {
	w.mu.Lock()
	wait, entered := w.wait, w.entered
	w.wait, w.entered = nil, nil
	w.mu.Unlock()
	if wait != nil {
		close(entered)
		select {
		case <-wait:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return w.Write(frame)
}

func TestClient_QueuedRunRejectsProviderActivityAtWriteBoundary(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	peer := newMockCodexServer()
	peer.handleRequest = func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T"}}`)}
		case "after/guard", "hold/writer":
		default:
			t.Errorf("queued execution sent an unexpected %s", req.Method)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	}
	writer := &gatedRequestWriter{WriteCloser: peer.clientOut}
	logger := sdklog.NewLoggerFromZap(nil)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(peer.serverIn), jsonrpc.NewLineWriter(writer), logger, jsonrpc.WithOrderedServerMessages())
	c, err := NewClient(ctx, types.NewCodexOptions())
	if err != nil {
		t.Fatal(err)
	}
	c.demux = demux
	c.connected.Store(true)
	c.dispatcherDone = make(chan struct{})
	c.dispatcherCancel = stop
	demux.Run(ctx)
	go c.dispatch(ctx, demux, c.dispatcherDone)
	defer func() {
		_ = c.Close(ctx)
		peer.close()
	}()
	ordered, err := c.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	held, release := make(chan struct{}), make(chan struct{})
	writer.mu.Lock()
	writer.wait, writer.entered = release, held
	writer.mu.Unlock()
	blocker := make(chan error, 1)
	go func() {
		_, holdErr := demux.Send(ctx, "hold/writer", nil)
		blocker <- holdErr
	}()
	awaitExecutionTest(t, ctx, held)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		_, runErr := thread.Run(runCtx, "must stay off wire", nil)
		runDone <- runErr
	}()
	awaitReservedOperation(t, ctx, thread)
	peer.push(json.RawMessage(`{"method":"thread/status/changed","params":{"threadId":"T","status":{"type":"active","activeFlags":[]}}}`))
	peer.push(notify("turn/started", map[string]string{"threadId": "T", "turnId": "external"}))
	awaitExecutionTest(t, ctx, ordered)
	awaitExecutionTest(t, ctx, ordered)
	close(release)
	if runErr := awaitExecutionTest(t, ctx, runDone); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("queued run dispatched after external start: %v", runErr)
	}
	cancel()
	if holdErr := awaitExecutionTest(t, ctx, blocker); holdErr != nil {
		t.Fatal(holdErr)
	}
	if _, guardErr := demux.Send(ctx, "after/guard", nil); guardErr != nil {
		t.Fatalf("connection unhealthy after guarded rejection: %v", guardErr)
	}
}

func awaitReservedOperation(t *testing.T, ctx context.Context, thread *Thread) {
	t.Helper()
	for {
		thread.mu.Lock()
		ready, changed := thread.op != nil, thread.changed
		thread.mu.Unlock()
		if ready {
			return
		}
		awaitExecutionTest(t, ctx, changed)
	}
}
