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

func TestClient_ReceivedProviderStartFencesOnlyItsThread(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	resumes, starts := 0, 0
	c, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			starts++
			if starts == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"blocker"}}`)}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"sibling"}}`)}
		case "thread/resume":
			resumes++
			if resumes == 1 {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"active"}}}`)}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T","status":{"type":"idle"}}}`)}
		case "turn/start":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil || params.ThreadID != "sibling" {
				t.Errorf("unapplied provider activity allowed a foreign turn/start: %s", req.Params)
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"sibling-turn"}}`)}
		case "wire/barrier":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		default:
			t.Errorf("unexpected method: %s", req.Method)
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	if _, err := c.ResumeThread(ctx, "T", nil); err != nil {
		t.Fatal(err)
	}
	target, err := c.ResumeThread(ctx, "T", nil)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The idle reply has already been applied. Hold ordered delivery on
	// another thread, then prove the demux has read the next external start.
	siblingEvents := func() <-chan types.ThreadEvent {
		blocker.mu.Lock()
		defer blocker.mu.Unlock()
		peer.push(notify("turn/started", map[string]string{"threadId": "blocker", "turnId": "hold"}))
		peer.push(json.RawMessage(`{"method":"turn/started","params":{"thread":{"id":"T"},"turn":{"id":"external"}}}`))
		if _, sendErr := c.demux.Send(ctx, "wire/barrier", nil); sendErr != nil {
			t.Fatal(sendErr)
		}
		if _, runErr := target.RunStreamed(ctx, "must not steer external work", nil); !errors.Is(runErr, ErrThreadExecutionUnconfirmed) {
			t.Fatalf("write did not account for the queued provider start: %v", runErr)
		}
		events, runErr := sibling.RunStreamed(ctx, "private unrelated work", nil)
		if runErr != nil {
			t.Fatalf("queued activity on another thread fenced the sibling: %v", runErr)
		}
		peer.push(notify("turn/completed", map[string]string{"threadId": "sibling", "turnId": "sibling-turn", "status": "completed"}))
		return events
	}()
	if terminal, ok := awaitExecutionTest(t, ctx, siblingEvents).(*types.TurnCompleted); !ok || terminal.TurnID != "sibling-turn" {
		t.Fatalf("private sibling terminal = %#v", terminal)
	}
}
