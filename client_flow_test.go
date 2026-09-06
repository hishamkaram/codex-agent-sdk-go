package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// mockCodexServer is an in-memory stand-in for `codex app-server`. The
// Client reads from serverOut and writes to clientOut. The test drives
// scripted responses via handleRequest + pushes notifications via send.
type mockCodexServer struct {
	clientIn  *io.PipeReader // client writes here (server reads)
	clientOut *io.PipeWriter
	serverIn  *io.PipeReader // server writes here (client reads)
	serverOut *io.PipeWriter

	mu       sync.Mutex
	received []json.RawMessage
	closed   bool

	// handleRequest receives every client-initiated request and must
	// return a response (Result or Error). notifications/server-requests
	// are driven from tests via push(). nil handleRequest => every request
	// gets an empty {"result":{}} back.
	handleRequest func(req jsonrpc.Request) jsonrpc.Response
}

func newMockCodexServer() *mockCodexServer {
	cir, ciw := io.Pipe()
	sor, sow := io.Pipe()
	s := &mockCodexServer{
		clientIn: cir, clientOut: ciw,
		serverIn: sor, serverOut: sow,
	}
	go s.readLoop()
	return s
}

func (s *mockCodexServer) readLoop() {
	lr := jsonrpc.NewLineReader(s.clientIn)
	for {
		line, err := lr.ReadLine()
		if err != nil {
			return
		}
		cp := make(json.RawMessage, len(line))
		copy(cp, line)
		s.mu.Lock()
		s.received = append(s.received, cp)
		s.mu.Unlock()

		// Decide whether this is a request (has id AND method) or a
		// response to a server-initiated request (has id, no method).
		var shape struct {
			ID     *uint64 `json:"id"`
			Method string  `json:"method"`
		}
		_ = json.Unmarshal(line, &shape)
		if shape.ID != nil && shape.Method != "" {
			// Client request — we respond.
			var req jsonrpc.Request
			_ = json.Unmarshal(line, &req)
			var resp jsonrpc.Response
			if s.handleRequest != nil {
				resp = s.handleRequest(req)
			} else {
				resp = jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
			}
			s.push(resp)
		}
	}
}

// push writes a raw JSON frame to the client.
func (s *mockCodexServer) push(v any) {
	data, _ := json.Marshal(v)
	data = append(data, '\n')
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	_, _ = s.serverOut.Write(data)
}

// close tears down the pipes.
func (s *mockCodexServer) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.serverOut.Close()
	_ = s.clientIn.Close()
}

type blockingFrameWriter struct {
	writeEntered chan struct{}
	releaseWrite chan struct{}
	enterOnce    sync.Once
	releaseOnce  sync.Once
}

func newBlockingFrameWriter() *blockingFrameWriter {
	return &blockingFrameWriter{
		writeEntered: make(chan struct{}),
		releaseWrite: make(chan struct{}),
	}
}

func (w *blockingFrameWriter) Write(_ []byte) (int, error) {
	w.enterOnce.Do(func() { close(w.writeEntered) })
	<-w.releaseWrite
	return 0, errors.New("blocking frame writer released")
}

func (w *blockingFrameWriter) Close() error {
	w.release()
	return nil
}

func (w *blockingFrameWriter) release() {
	w.releaseOnce.Do(func() { close(w.releaseWrite) })
}

// setupMockClient wires a Client to a mockCodexServer without spawning a
// real subprocess. Returns a ready Client and the server. Runs Connect
// through by injecting an initialize-response handler.
func setupMockClient(t *testing.T, opts *types.CodexOptions, handleRequest func(jsonrpc.Request) jsonrpc.Response) (*Client, *mockCodexServer) {
	t.Helper()
	srv := newMockCodexServer()
	srv.handleRequest = handleRequest

	lr := jsonrpc.NewLineReader(srv.serverIn)
	lw := jsonrpc.NewLineWriter(srv.clientOut)
	logger := sdklog.NewLoggerFromZap(nil)
	demux := jsonrpc.NewDemux(lr, lw, logger, jsonrpc.WithOrderedServerMessages())
	demux.Run(context.Background())

	c := &Client{
		opts:    opts,
		logger:  logger,
		demux:   demux,
		threads: make(map[string]*Thread),
	}
	c.connected.Store(true)
	dispatcherCtx, dispatcherCancel := context.WithCancel(context.Background())
	c.dispatcherCtx = dispatcherCtx
	c.dispatcherCancel = dispatcherCancel
	c.dispatcherDone = make(chan struct{})
	go c.dispatch(dispatcherCtx, demux, c.dispatcherDone)

	t.Cleanup(func() {
		_ = c.Close(context.Background())
		srv.close()
	})
	return c, srv
}

// helper to serialize a notification as a raw JSON-RPC frame for push.
func notify(method string, params any) map[string]any {
	return map[string]any{"method": method, "params": params}
}

func TestClient_StartThreadAndRun_HappyPath(t *testing.T) {
	t.Parallel()

	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{
				ID:     req.ID,
				Result: json.RawMessage(`{"thread":{"id":"T-xyz"},"model":"gpt-5.4"}`),
			}
		case "turn/start":
			return jsonrpc.Response{
				ID:     req.ID,
				Result: json.RawMessage(`{"turn":{"id":"U-abc"}}`),
			}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID() != "T-xyz" {
		t.Fatalf("thread id = %q", thread.ID())
	}

	// Start a turn (returns streamed events channel).
	events, err := thread.RunStreamed(ctx, "hi", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Server emits turn lifecycle.
	srv.push(notify("turn/started", map[string]any{"threadId": "T-xyz", "turnId": "U-abc"}))
	srv.push(notify("item/started", map[string]any{
		"threadId": "T-xyz", "turnId": "U-abc", "itemId": "I-1",
		"item": map[string]any{"type": "agentMessage", "text": ""},
	}))
	srv.push(notify("item/agentMessage/delta", map[string]any{
		"threadId": "T-xyz", "turnId": "U-abc", "itemId": "I-1", "delta": "Hello",
	}))
	srv.push(notify("item/completed", map[string]any{
		"threadId": "T-xyz", "turnId": "U-abc", "itemId": "I-1",
		"item": map[string]any{"type": "agentMessage", "text": "Hello"},
	}))
	srv.push(notify("turn/completed", map[string]any{
		"threadId": "T-xyz", "turnId": "U-abc", "status": "success",
		"usage": map[string]any{"inputTokens": 10, "outputTokens": 5},
	}))

	// Drain.
	var saw struct {
		turnStarted   bool
		itemStarted   bool
		itemUpdated   bool
		itemCompleted bool
		turnCompleted bool
	}
	for ev := range events {
		switch e := ev.(type) {
		case *types.TurnStarted:
			if e.TurnID != "U-abc" {
				t.Fatalf("turn id = %q", e.TurnID)
			}
			saw.turnStarted = true
		case *types.ItemStarted:
			saw.itemStarted = true
		case *types.ItemUpdated:
			saw.itemUpdated = true
			if d, ok := e.Delta.(*types.AgentMessageDelta); !ok || d.TextChunk != "Hello" {
				t.Fatalf("delta = %+v", e.Delta)
			}
		case *types.ItemCompleted:
			saw.itemCompleted = true
			if msg, ok := e.Item.(*types.AgentMessage); !ok || msg.Text != "Hello" {
				t.Fatalf("item = %+v", e.Item)
			}
		case *types.TurnCompleted:
			saw.turnCompleted = true
			if e.Status != "success" {
				t.Fatalf("status = %q", e.Status)
			}
			if e.Usage.InputTokens != 10 || e.Usage.OutputTokens != 5 {
				t.Fatalf("usage = %+v", e.Usage)
			}
		}
	}
	if !saw.turnStarted || !saw.itemStarted || !saw.itemUpdated || !saw.itemCompleted || !saw.turnCompleted {
		t.Fatalf("missing events: %+v", saw)
	}
}

func TestClient_CloseLeavesDemuxStableForConcurrentCallers(t *testing.T) {
	t.Parallel()

	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[]}`)}
	})
	_ = srv
	originalDemux := c.demux
	if originalDemux == nil {
		t.Fatal("mock client demux is nil before Close")
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.demux != originalDemux {
		t.Fatal("Close changed c.demux; concurrent public send paths require a stable pointer")
	}
}

func TestClient_CloseClosesActiveRunStreamed(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{
				ID:     req.ID,
				Result: json.RawMessage(`{"thread":{"id":"T-close"}}`),
			}
		case "turn/start":
			return jsonrpc.Response{
				ID:     req.ID,
				Result: json.RawMessage(`{"turn":{"id":"U-close"}}`),
			}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := thread.RunStreamed(ctx, "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("RunStreamed channel stayed open after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunStreamed channel did not close after Close")
	}

	if _, err := thread.RunStreamed(ctx, "again", nil); !errors.Is(err, types.ErrThreadClosed) {
		t.Fatalf("RunStreamed after Close error = %v, want ErrThreadClosed", err)
	}
}

func TestClient_StartReviewStreamed_HappyPath(t *testing.T) {
	t.Parallel()

	var sawReviewStart bool
	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T-review"}}`)}
		case "review/start":
			sawReviewStart = true
			var params types.ReviewStartParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: err.Error()}}
			}
			if params.ThreadID != "T-review" || params.Target.Type != "uncommittedChanges" || params.Delivery != types.ReviewInline {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "unexpected review params"}}
			}
			return jsonrpc.Response{
				ID:     req.ID,
				Result: json.RawMessage(`{"reviewThreadId":"T-review","turn":{"id":"R-1","status":"running"}}`),
			}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	events, err := thread.StartReviewStreamed(ctx, types.ReviewOptions{
		Target:   types.ReviewTargetUncommittedChanges(),
		Delivery: types.ReviewInline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawReviewStart {
		t.Fatal("review/start was not called")
	}

	srv.push(notify("turn/started", map[string]any{"threadId": "T-review", "turnId": "R-1"}))
	srv.push(notify("item/completed", map[string]any{
		"threadId": "T-review", "turnId": "R-1", "itemId": "I-review",
		"item": map[string]any{"type": "agentMessage", "text": "Review finding"},
	}))
	srv.push(notify("turn/completed", map[string]any{
		"threadId": "T-review", "turnId": "R-1", "status": "success",
	}))

	var sawText bool
	for ev := range events {
		if item, ok := ev.(*types.ItemCompleted); ok {
			msg, ok := item.Item.(*types.AgentMessage)
			if ok && msg.Text == "Review finding" {
				sawText = true
			}
		}
	}
	if !sawText {
		t.Fatal("review stream did not include review output")
	}
}

func TestClient_ThreadRun_BufferedTurnResult(t *testing.T) {
	t.Parallel()

	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T1"}}`)}
		case "turn/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"U1"}}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Emit turn events asynchronously so Run can start consuming.
	go func() {
		srv.push(notify("turn/started", map[string]any{"threadId": "T1", "turnId": "U1"}))
		srv.push(notify("item/completed", map[string]any{
			"threadId": "T1", "turnId": "U1", "itemId": "I-1",
			"item": map[string]any{"type": "agentMessage", "text": "Final answer"},
		}))
		srv.push(notify("turn/completed", map[string]any{
			"threadId": "T1", "turnId": "U1", "status": "success",
			"usage": map[string]any{"inputTokens": 3, "outputTokens": 7},
		}))
	}()

	turn, err := thread.Run(ctx, "what", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID != "U1" || turn.ThreadID != "T1" || turn.Status != "success" {
		t.Fatalf("turn = %+v", turn)
	}
	if turn.FinalResponse != "Final answer" {
		t.Fatalf("final = %q", turn.FinalResponse)
	}
	if turn.Usage.InputTokens != 3 || turn.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", turn.Usage)
	}
	if len(turn.Items) != 1 {
		t.Fatalf("items = %d", len(turn.Items))
	}
}

func TestClient_ApprovalCallback_RoundTrip(t *testing.T) {
	t.Parallel()

	gotApproval := make(chan types.ApprovalRequest, 1)
	opts := types.NewCodexOptions().
		WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
			gotApproval <- req
			return types.ApprovalAccept{}
		})

	c, srv := setupMockClient(t, opts, func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T1"}}`)}
	})
	_ = c

	// Push a server-initiated approval request at id=999.
	srv.push(map[string]any{
		"id":     999,
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"command": "rm -rf /", "reason": "destructive"},
	})

	// Wait for the callback to fire.
	select {
	case req := <-gotApproval:
		ce, ok := req.(*types.CommandExecutionApprovalRequest)
		if !ok {
			t.Fatalf("got %T", req)
		}
		if ce.Command != "rm -rf /" {
			t.Fatalf("command = %q", ce.Command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval callback did not fire within 2s")
	}

	// The Client should have sent a response back with id=999.
	// Drain: poll the server's received buffer for a frame with id=999
	// and result field.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		rx := make([]json.RawMessage, len(srv.received))
		copy(rx, srv.received)
		srv.mu.Unlock()
		for _, line := range rx {
			if strings.Contains(string(line), `"id":999`) && strings.Contains(string(line), `"result"`) {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("approval response never reached the server")
	}
}

func TestClient_CloseWhileApprovalCallbackReentersHealth(t *testing.T) {
	t.Parallel()

	enteredApproval := make(chan struct{})
	allowApprovalReturn := make(chan struct{})
	var clientRef *Client
	opts := types.NewCodexOptions().
		WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
			close(enteredApproval)
			_ = clientRef.Health()
			select {
			case <-allowApprovalReturn:
			case <-ctx.Done():
			}
			return types.ApprovalDeny{Reason: "closing"}
		})

	c, srv := setupMockClient(t, opts, func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})
	clientRef = c

	srv.push(map[string]any{
		"id":     999,
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"command": "touch file", "reason": "test"},
	})

	select {
	case <-enteredApproval:
	case <-time.After(2 * time.Second):
		t.Fatal("approval callback did not fire")
	}

	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closeDone <- c.Close(ctx)
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(allowApprovalReturn)
		t.Fatal("Close hung while approval callback re-entered Health")
	}
}

func TestClient_CloseUnblocksApprovalResponseWrite(t *testing.T) {
	t.Parallel()

	srv := newMockCodexServer()
	writer := newBlockingFrameWriter()
	logger := sdklog.NewLoggerFromZap(nil)
	demux := jsonrpc.NewDemux(
		jsonrpc.NewLineReader(srv.serverIn),
		jsonrpc.NewLineWriter(writer),
		logger,
	)
	demux.Run(context.Background())

	enteredApproval := make(chan struct{})
	var enteredOnce sync.Once
	opts := types.NewCodexOptions().
		WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
			enteredOnce.Do(func() { close(enteredApproval) })
			return types.ApprovalDeny{Reason: "closing"}
		})

	c := &Client{
		opts:    opts,
		logger:  logger,
		demux:   demux,
		threads: make(map[string]*Thread),
	}
	c.connected.Store(true)
	dispatcherCtx, dispatcherCancel := context.WithCancel(context.Background())
	c.dispatcherCtx = dispatcherCtx
	c.dispatcherCancel = dispatcherCancel
	c.dispatcherDone = make(chan struct{})
	go c.dispatch(dispatcherCtx, demux, c.dispatcherDone)

	t.Cleanup(func() {
		writer.release()
		dispatcherCancel()
		_ = demux.Close()
		srv.close()
		select {
		case <-c.dispatcherDone:
		case <-time.After(2 * time.Second):
			t.Fatal("dispatcher did not exit during cleanup")
		}
	})

	srv.push(map[string]any{
		"id":     999,
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"command": "touch file", "reason": "test"},
	})

	select {
	case <-enteredApproval:
	case <-time.After(2 * time.Second):
		t.Fatal("approval callback did not fire")
	}
	select {
	case <-writer.writeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("approval response write did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestClient_ArchiveThreadUnregisters(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"T-arch"}}`)}
		case "thread/archive":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	ctx := context.Background()
	thread, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	_, present := c.threads[thread.ID()]
	c.mu.Unlock()
	if !present {
		t.Fatal("thread missing from routing table after StartThread")
	}

	if err := c.ArchiveThread(ctx, thread.ID()); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	_, present = c.threads[thread.ID()]
	c.mu.Unlock()
	if present {
		t.Fatal("thread still in routing table after ArchiveThread")
	}
}
