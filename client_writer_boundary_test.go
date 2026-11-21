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
	"github.com/hishamkaram/codex-agent-sdk-go/internal/threadexec"
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
	if startErr := c.session.StartDispatcher(ctx, stop, demux, c.session.Dispatch); startErr != nil {
		t.Fatal(startErr)
	}
	demux.Run(ctx)
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
	atWriter := make(chan struct{}, 1)
	go func() {
		_, runErr := thread.Run(withWriterWait(runCtx, atWriter), "must stay off wire", nil)
		runDone <- runErr
	}()
	awaitExecutionTest(t, ctx, atWriter)
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

// The first Done read on this uncontended admission is the writer's
// slot select. The holder already has the slot when Run enters it.
type writerWaitContext struct {
	context.Context
	entered chan<- struct{}
}

func withWriterWait(ctx context.Context, entered chan<- struct{}) context.Context {
	return writerWaitContext{Context: ctx, entered: entered}
}

func (ctx writerWaitContext) Done() <-chan struct{} {
	select {
	case ctx.entered <- struct{}{}:
	default:
	}
	return ctx.Context.Done()
}

// Hold the owner at the pre-write receipt check so the production
// dispatcher can rendezvous without taking a private record mutex.
func holdExecutionGuard(t *testing.T, ctx context.Context, thread *Thread) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() { close(release) })
		<-done
	}
	t.Cleanup(func() {
		cancel()
		unblock()
	})
	rejected := errors.New("test held execution")
	wire := thread.executionWire()
	wire.PostGuarded = func(ctx context.Context, _ string, _ any, guard func(context.Context) error) (*jsonrpc.PendingResponse, error) {
		return nil, guard(ctx)
	}
	wire.PendingActivity = func() (bool, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return false, rejected
	}
	go func() {
		defer close(done)
		_, _, _ = thread.record.Begin(ctx, thread.ID(), threadexec.Turn, false, "turn/start", nil, wire)
	}()
	awaitExecutionTest(t, ctx, entered)
	return unblock
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
		release := holdExecutionGuard(t, ctx, blocker)
		defer release()
		peer.push(notify("turn/started", map[string]string{"threadId": "blocker", "turnId": "hold"}))
		peer.push(json.RawMessage(`{"method":"turn/started","params":{"thread":{"id":"T"},"turn":{"id":"external"}}}`))
		if _, sendErr := c.session.Demux().Send(ctx, "wire/barrier", nil); sendErr != nil {
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
