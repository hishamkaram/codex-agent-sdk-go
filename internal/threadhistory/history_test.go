package threadhistory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestReadThreadClassifiesProviderError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		message  string
		wantCode string
	}{
		{"missing thread", "Thread not found", types.ThreadHistoryNotFound},
		{"unsupported command", "Unknown method", types.ThreadHistoryFeatureUnavailable},
		{"generic error", "Provider error", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			send := func(_ context.Context, method string, params any) (jsonrpc.Response, error) {
				if method != "thread/read" || !reflect.DeepEqual(params, map[string]any{"threadId": "test-id"}) {
					t.Fatalf("request = %q %#v, want thread/read without includeTurns", method, params)
				}
				return jsonrpc.Response{
					Error: &jsonrpc.RPCError{Code: -32601, Message: tt.message, Data: json.RawMessage(`{"test":true}`)},
				}, nil
			}
			read, err := ReadThread(t.Context(), send, "test-id", nil)
			var rpc *types.RPCError
			if read != nil || !errors.As(err, &rpc) || rpc.Code != -32601 || rpc.Message != tt.message {
				t.Fatalf("read = %+v, err = %#v, want original RPC failure", read, err)
			}
			var history *types.ThreadHistoryError
			if errors.As(err, &history) != (tt.wantCode != "") {
				t.Fatalf("err = %#v, want history code %q", err, tt.wantCode)
			}
			if history != nil && (history.Code != tt.wantCode || history.ThreadID != "test-id") {
				t.Fatalf("history error = %+v, want code %q for test-id", history, tt.wantCode)
			}
		})
	}
}

func TestListThreadsPageRetainsLegacyRowsAndRawCopy(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"data":[],"threads":[{"sessionId":"legacy-id","name":"legacy name"}],"nextCursor":"next"}`)
	wantRaw := string(raw)
	send := func(_ context.Context, method string, params any) (jsonrpc.Response, error) {
		if method != "thread/list" || !reflect.DeepEqual(params, map[string]any{}) {
			t.Fatalf("request = %q %#v, want thread/list with empty object", method, params)
		}
		return jsonrpc.Response{Result: raw}, nil
	}
	page, err := ListThreadsPage(t.Context(), send, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if page == nil || len(page.Threads) != 1 || page.Threads[0].ThreadID != "legacy-id" || page.Threads[0].Summary != "legacy name" ||
		page.NextCursor != "next" || string(page.Raw) != wantRaw {
		t.Fatalf("page = %+v, want legacy row, cursor, and independently owned raw reply", page)
	}
}

func TestReadThreadProjectsWindowAcrossTurnsWithIndependentRaw(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"thread":{"sessionId":"saved-id","turns":[
		{"id":"first","items":[
			{"id":"u1","type":"userMessage","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]},
			{"id":"c1","type":"commandExecution","command":"pwd"}
		]},
		{"id":"second","items":[{"id":"a1","type":"agentMessage","text":"reply","future":true}]}
	]}}`)
	wantRaw := string(raw)
	send := func(_ context.Context, method string, params any) (jsonrpc.Response, error) {
		want := map[string]any{"threadId": "saved-id", "includeTurns": true}
		if method != "thread/read" || !reflect.DeepEqual(params, want) {
			t.Fatalf("request = %q %#v, want thread/read %#v", method, params, want)
		}
		return jsonrpc.Response{Result: raw}, nil
	}
	read, err := ReadThread(t.Context(), send, "saved-id", &types.ThreadReadOptions{IncludeTurns: true})
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if read.Thread.ID != "saved-id" || string(read.Raw) != wantRaw {
		t.Fatalf("read = %+v, want session ID fallback and an independent raw copy", read)
	}

	messages := Messages(read.Thread, nil)
	if len(messages) != 2 || messages[0].Text != "one\ntwo" || messages[0].ID != "u1" {
		t.Fatalf("messages = %+v, want two messages with joined user text", messages)
	}
	window := Messages(read.Thread, &types.GetThreadMessagesOptions{Offset: 1, Limit: 1})
	const wantMessageRaw = `{"id":"a1","type":"agentMessage","text":"reply","future":true}`
	if len(window) != 1 || window[0].Role != "assistant" || window[0].ThreadID != "saved-id" ||
		window[0].TurnID != "second" || window[0].Text != "reply" || string(window[0].Raw) != wantMessageRaw {
		t.Fatalf("window = %+v, want assistant reply from the second turn with raw fields", window)
	}
	window[0].Raw[0] = '!'
	if rawItem := read.Thread.Turns[1].Items[0].Raw; string(rawItem) != wantMessageRaw {
		t.Fatalf("message raw bytes changed history item: %q", rawItem)
	}
}
