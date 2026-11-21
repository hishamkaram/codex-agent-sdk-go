package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestReadThreadAndMessagesUseThreadReadOnly(t *testing.T) {
	t.Parallel()
	var methods []string
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		methods = append(methods, req.Method)
		if req.Method != "thread/read" {
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
		}
		var params map[string]any
		_ = json.Unmarshal(req.Params, &params)
		if params["threadId"] != "T1" || params["includeTurns"] != true {
			t.Fatalf("thread/read params = %+v", params)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{
			"thread":{
				"id":"T1",
				"sessionId":"T1",
				"cwd":"/repo",
				"turns":[{
					"id":"turn-1",
					"status":"success",
					"items":[
						{"type":"userMessage","id":"u1","content":[{"type":"text","text":"hello"}]},
						{"type":"agentMessage","id":"a1","text":"world"},
						{"type":"commandExecution","id":"cmd1","command":"pwd"}
					]
				}]
			}
		}`)}
	})

	messages, err := c.GetThreadMessages(context.Background(), "T1", nil)
	if err != nil {
		t.Fatalf("GetThreadMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Text != "hello" {
		t.Fatalf("first message = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Text != "world" {
		t.Fatalf("second message = %+v", messages[1])
	}
	for _, method := range methods {
		switch method {
		case "thread/read":
		case "thread/resume", "thread/start", "turn/start":
			t.Fatalf("history read called mutating/live method %q", method)
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}
}

func TestListThreadsPageParsesPagedDataAndParams(t *testing.T) {
	t.Parallel()
	archived := true
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method != "thread/list" {
			return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
		}
		var params map[string]any
		_ = json.Unmarshal(req.Params, &params)
		if params["limit"] != float64(25) || params["cursor"] != "cur-1" || params["archived"] != true || params["searchTerm"] != "api" {
			t.Fatalf("thread/list params = %+v", params)
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{
			"data":[{"id":"T1","name":"First","preview":"hello","cwd":"/repo","updatedAt":"2026-05-22T12:00:00Z"}],
			"nextCursor":"cur-2",
			"backwardsCursor":"cur-0"
		}`)}
	})

	page, err := c.ListThreadsPage(context.Background(), &types.ThreadListOptions{
		Limit:      25,
		Cursor:     "cur-1",
		Archived:   &archived,
		SearchTerm: "api",
	})
	if err != nil {
		t.Fatalf("ListThreadsPage: %v", err)
	}
	if page.NextCursor != "cur-2" || page.BackwardsCursor != "cur-0" {
		t.Fatalf("cursors = %q/%q", page.NextCursor, page.BackwardsCursor)
	}
	if len(page.Threads) != 1 || page.Threads[0].ThreadID != "T1" || page.Threads[0].Summary != "hello" || len(page.Threads[0].Raw) == 0 {
		t.Fatalf("threads = %+v", page.Threads)
	}
}

func TestReadThreadInvalidID(t *testing.T) {
	t.Parallel()
	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	_, err := c.ReadThread(context.Background(), "", nil)
	if !types.IsInvalidThreadIDError(err) {
		t.Fatalf("ReadThread empty err = %v, want invalid thread id", err)
	}
}
