package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestListBackgroundTerminalsPaginatesExactPayload(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var cursors []string
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		if req.Method != "thread/backgroundTerminals/list" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		}
		var params struct {
			ThreadID string  `json:"threadId"`
			Cursor   *string `json:"cursor"`
			Limit    uint32  `json:"limit"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("decode params: %v", err)
		}
		if params.ThreadID != "thread-child" || params.Limit != 100 {
			t.Errorf("params = %+v", params)
		}
		cursor := ""
		if params.Cursor != nil {
			cursor = *params.Cursor
		}
		mu.Lock()
		cursors = append(cursors, cursor)
		mu.Unlock()
		if cursor == "" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"command":"sleep 10","cwd":"/tmp","itemId":"item-1","processId":"process-1","osPid":12,"cpuPercent":0.5,"rssKb":1024}],"nextCursor":"next"}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[{"command":"tail -f log","cwd":"/work","itemId":"item-2","processId":"process-2"}],"nextCursor":null}`)}
	})

	terminals, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
	if err != nil {
		t.Fatalf("ListBackgroundTerminals: %v", err)
	}
	if len(terminals) != 2 || terminals[0].ProcessID != "process-1" || terminals[1].Command != "tail -f log" {
		t.Fatalf("terminals = %+v", terminals)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "next" {
		t.Fatalf("cursors = %v", cursors)
	}
}

func TestListBackgroundTerminalsRejectsCursorCycle(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"data":[],"nextCursor":"again"}`)}
	})
	_, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
	if !errors.Is(err, ErrBackgroundTerminalPagination) {
		t.Fatalf("error = %v", err)
	}
}

func TestListBackgroundTerminalsRejectsMissingOrNullData(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`{}`, `null`, `{"data":null}`} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(raw)}
			})
			_, err := c.ListBackgroundTerminals(context.Background(), "thread-child")
			if err == nil || !types.IsJSONDecodeError(err) {
				t.Fatalf("error = %v, want JSONDecodeError", err)
			}
		})
	}
}

func TestTerminateBackgroundTerminalAndInterruptThreadTurnPayloads(t *testing.T) {
	t.Parallel()

	seen := make(map[string]map[string]string)
	var mu sync.Mutex
	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		var params map[string]string
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Errorf("decode %s params: %v", req.Method, err)
		}
		mu.Lock()
		seen[req.Method] = params
		mu.Unlock()
		if req.Method == "thread/backgroundTerminals/terminate" {
			return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"terminated":true}`)}
		}
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
	})

	if err := c.TerminateBackgroundTerminal(context.Background(), "thread-child", "process-1"); err != nil {
		t.Fatalf("TerminateBackgroundTerminal: %v", err)
	}
	if err := c.InterruptThreadTurn(context.Background(), "thread-child", "turn-child"); err != nil {
		t.Fatalf("InterruptThreadTurn: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := seen["thread/backgroundTerminals/terminate"]; got["threadId"] != "thread-child" || got["processId"] != "process-1" || len(got) != 2 {
		t.Fatalf("terminate params = %#v", got)
	}
	if got := seen["turn/interrupt"]; got["threadId"] != "thread-child" || got["turnId"] != "turn-child" || len(got) != 2 {
		t.Fatalf("interrupt params = %#v", got)
	}
}

func TestTerminateBackgroundTerminalWrapsNegativeAcknowledgement(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), func(req jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"terminated":false}`)}
	})
	err := c.TerminateBackgroundTerminal(context.Background(), "thread-child", "process-1")
	if !errors.Is(err, ErrBackgroundTerminalNotTerminated) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "codex.Client.TerminateBackgroundTerminal") {
		t.Fatalf("error lacks operation context: %v", err)
	}
}
