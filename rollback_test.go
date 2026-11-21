package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestThreadRollbackPreservesLegacyRoute(t *testing.T) {
	t.Parallel()

	for _, wireError := range []*jsonrpc.RPCError{
		nil,
		{Code: -32600, Message: "Invalid request", Data: json.RawMessage(`{"detail":"active"}`)},
		{Code: -32000, Message: "rollback failed"},
	} {
		name := "success"
		if wireError != nil {
			name = wireError.Message
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				if req.Method == "thread/start" {
					return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"source"}}`)}
				}
				if req.Method == "thread/rollback" && wireError != nil {
					return jsonrpc.Response{ID: req.ID, Error: wireError}
				}
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
			})
			thread, err := client.StartThread(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = thread.Rollback(t.Context(), 2)
			if wireError == nil && err != nil {
				t.Fatal(err)
			}
			var rpc *types.RPCError
			if wireError != nil && (!errors.As(err, &rpc) || rpc.Code != wireError.Code ||
				rpc.Message != wireError.Message || string(rpc.Data) != string(wireError.Data)) {
				t.Fatalf("Rollback error = %v, want provider error %v", err, wireError)
			}
			wire := observedThreadRequests(t, peer)
			assertThreadRequestMethods(t, wire, "thread/start", "thread/rollback")
			assertThreadConfigJSON(t, wire[1].Params, `{"threadId":"source","numTurns":2}`)
		})
	}
}

func TestThreadRollbackFindsExclusiveBoundaryAcrossPages(t *testing.T) {
	t.Parallel()
	client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		result := `{}`
		switch req.Method {
		case "thread/start":
			result = `{"thread":{"id":"source"}}`
		case "thread/rollback":
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{
				Code: -32600, Message: "Invalid request: unknown variant `thread/rollback`, expected one of `thread/revert`",
			}}
		case "thread/turns/list":
			var page struct {
				Cursor string `json:"cursor"`
			}
			if json.Unmarshal(req.Params, &page) == nil && page.Cursor == "a /+==" {
				result = `{"data":[{"id":"older","status":"interrupted"}],"nextCursor":"unused"}`
			} else {
				result = `{"data":[{"id":"newest","status":"completed"}],"nextCursor":"a /+=="}`
			}
		case "thread/revert":
			result = `{"thread":{"id":"source","turns":[]}}`
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(result)}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := thread.Rollback(ctx, 2); err != nil {
		t.Fatal(err)
	}
	wire := observedThreadRequests(t, peer)
	assertThreadRequestMethods(t, wire, "thread/start", "thread/rollback", "thread/turns/list", "thread/turns/list", "thread/revert")
	assertThreadConfigJSON(t, wire[2].Params, `{"threadId":"source","sortDirection":"desc","itemsView":"notLoaded","limit":2}`)
	assertThreadConfigJSON(t, wire[3].Params, `{"threadId":"source","sortDirection":"desc","itemsView":"notLoaded","limit":1,"cursor":"a /+=="}`)
	assertThreadConfigJSON(t, wire[4].Params, `{"threadId":"source","beforeTurnId":"older"}`)
}

func TestThreadRollbackRejectsUnprovedBoundary(t *testing.T) {
	t.Parallel()
	for _, page := range []string{
		`{"data":[{"id":"one","status":"completed"}],"nextCursor":null}`,
		`{"data":[{"id":"one","status":"completed"}],"nextCursor":"repeat"}`,
		`{"data":[{"id":"one","status":"inProgress"}],"nextCursor":null}`,
		`{"data":null}`,
	} {
		t.Run(page, func(t *testing.T) {
			t.Parallel()
			client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				result := `{}`
				switch req.Method {
				case "thread/start":
					result = `{"thread":{"id":"source"}}`
				case "thread/rollback":
					return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "Method not found"}}
				case "thread/turns/list":
					result = page
				}
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(result)}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			thread, err := client.StartThread(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := thread.Rollback(ctx, 2); err == nil {
				t.Fatal("Rollback accepted an incomplete boundary")
			}
			requests := observedThreadRequests(t, peer)
			for _, request := range requests {
				if request.Method == "thread/revert" {
					t.Fatal("incomplete boundary caused a history mutation")
				}
			}
		})
	}
}

func TestThreadRollbackFencesAfterCanceledMutation(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	dispatched := make(chan jsonrpc.Request, 1)
	client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"source"}}`)}
		case "thread/rollback":
			dispatched <- req
			return jsonrpc.Response{}
		default:
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
	})
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rollbackCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- thread.Rollback(rollbackCtx, 1)
	}()
	request := awaitExecutionTest(t, ctx, dispatched)
	cancel()
	if err := awaitExecutionTest(t, ctx, finished); !errors.Is(err, context.Canceled) {
		t.Fatalf("Rollback error = %v, want context.Canceled", err)
	}
	// The late error cannot start a fallback after the caller has left.
	peer.push(jsonrpc.Response{ID: request.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "Method not found"}})
	if _, err := thread.RunStreamed(ctx, "must stay fenced", nil); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("RunStreamed error = %v, want ErrThreadExecutionUnconfirmed", err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, peer), "thread/start", "thread/rollback")
}

func TestThreadRollbackRejectsStaleBoundaryAfterExternalTurn(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	listed := make(chan struct{})
	releaseList := make(chan struct{})
	client, peer := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		result := `{}`
		switch req.Method {
		case "thread/start", "thread/resume":
			result = `{"thread":{"id":"source","status":{"type":"idle"}}}`
		case "thread/rollback":
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "Method not found"}}
		case "thread/turns/list":
			close(listed)
			select {
			case <-releaseList:
			case <-ctx.Done():
			}
			result = `{"data":[{"id":"stale","status":"completed"}]}`
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(result)}
	})
	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := client.ResumeThread(ctx, thread.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.SubscribeThreadEvents(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- thread.Rollback(ctx, 1)
	}()
	awaitExecutionTest(t, ctx, listed)

	// Another SDK handle is admitted through the same record while the
	// rollback owns the snapshot and may not send a turn of its own.
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := alias.RunStreamed(short, "must wait for rollback", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunStreamed error = %v, want deadline while rollback holds admission", err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, peer), "thread/start", "thread/resume", "thread/rollback", "thread/turns/list")

	// Even if an external turn completes before dispatch, the generation
	// changed after the snapshot began and the old suffix is unsafe.
	peer.push(json.RawMessage(`{"method":"turn/started","params":{"threadId":"source","turn":{"id":"external"}}}`))
	awaitExecutionTest(t, ctx, events)
	peer.push(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"source","turn":{"id":"external","status":"completed"}}}`))
	awaitExecutionTest(t, ctx, events)
	close(releaseList)
	if err := awaitExecutionTest(t, ctx, finished); !errors.Is(err, ErrThreadExecutionUnconfirmed) {
		t.Fatalf("Rollback error = %v, want unconfirmed stale snapshot", err)
	}
	assertThreadRequestMethods(t, observedThreadRequests(t, peer), "thread/start", "thread/resume", "thread/rollback", "thread/turns/list")
}
