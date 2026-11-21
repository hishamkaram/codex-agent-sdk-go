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

func TestClient_ForkThread_ProviderContract(t *testing.T) {
	t.Parallel()

	c, srv := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		switch req.Method {
		case "thread/start":
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
		case "thread/fork":
			// Independent v2 contract. A state-creating call using the old
			// spelling must fail rather than being accepted by the fake.
			var params struct {
				ThreadID       string          `json:"threadId"`
				SourceThreadID json.RawMessage `json:"sourceThreadId"`
				Model          string          `json:"model"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.ThreadID == "" || params.SourceThreadID != nil {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32602, Message: "threadId is required"}}
			}
			if params.ThreadID == "parent" && params.Model == "invalid-child" {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent"}}`)}
			}
			if params.ThreadID != "parent" || params.Model != "gpt-5.4" {
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32000, Message: "unsupported fork options"}}
			}
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"child"},"model":"gpt-5.4"}`)}
		case "turn/start":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			if json.Unmarshal(req.Params, &params) == nil && params.ThreadID == "parent" {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"parent-turn"}}`)}
			}
		}
		return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected request"}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	parent, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	child, fork, err := c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if child.ID() != "child" || parent.ID() != "parent" || fork.SourceThreadID != parent.ID() || fork.NewThreadID != child.ID() {
		t.Fatalf("parent = %q, child = %q, fork = %+v", parent.ID(), child.ID(), fork)
	}
	if _, _, err = c.ForkThread(ctx, "", nil); err == nil {
		t.Fatal("fork without a source was accepted")
	}
	_, _, err = c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "unsupported"})
	var rpcErr *types.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32000 || rpcErr.Message != "unsupported fork options" {
		t.Fatalf("provider rejection = %v", err)
	}
	if _, _, err = c.ForkThread(ctx, parent.ID(), &types.ThreadOptions{Model: "invalid-child"}); err == nil {
		t.Fatal("fork response with the parent ID was accepted as a child")
	}

	stream, err := parent.RunStreamed(ctx, "continue original", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.push(json.RawMessage(`{"method":"turn/completed","params":{"threadId":"parent","turn":{"id":"parent-turn","status":"completed","items":[]}}}`))
	select {
	case event := <-stream:
		completed, ok := event.(*types.TurnCompleted)
		if !ok || completed.TurnID != "parent-turn" {
			t.Fatalf("parent completion = %#v", event)
		}
	case <-ctx.Done():
		t.Fatal("parent did not receive its completion")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	var forks int
	for _, frame := range srv.received {
		var req jsonrpc.Request
		if json.Unmarshal(frame, &req) == nil && req.Method == "thread/fork" {
			forks++
		}
	}
	if forks != 3 {
		t.Fatalf("got %d fork requests, want only the three attempts with a source", forks)
	}
}
