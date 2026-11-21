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

func TestListThreadsPagePreservesCwdWireShapeAndFilters(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		cwd     []string
		wantCwd string
	}{
		{name: "one path", cwd: []string{"/repo"}, wantCwd: `"/repo"`},
		{name: "two paths", cwd: []string{"/repo", "/other"}, wantCwd: `["/repo","/other"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			archived := false
			opts := &types.ThreadListOptions{
				Limit: 2, Cursor: "cursor", Archived: &archived, Cwd: tc.cwd, SearchTerm: "prior work",
				SortKey: "updated_at", SortDirection: "desc", ModelProviders: []string{"openai"},
				SourceKinds: []string{"cli"}, UseStateDBOnly: true,
			}
			send := func(_ context.Context, method string, params any) (jsonrpc.Response, error) {
				wire, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				want := `{"archived":false,"cursor":"cursor","cwd":` + tc.wantCwd +
					`,"limit":2,"modelProviders":["openai"],"searchTerm":"prior work","sortDirection":"desc",` +
					`"sortKey":"updated_at","sourceKinds":["cli"],"useStateDbOnly":true}`
				if method != "thread/list" || string(wire) != want {
					t.Fatalf("request = %q %s, want thread/list %s", method, wire, want)
				}
				return jsonrpc.Response{Result: json.RawMessage(`{"data":[],"backwardsCursor":"back"}`)}, nil
			}
			page, err := ListThreadsPage(t.Context(), send, opts)
			if err != nil || page == nil || page.BackwardsCursor != "back" {
				t.Fatalf("page = %+v, err = %v, want provider backwards cursor", page, err)
			}
		})
	}
}

func TestReadThreadReportsMalformedProviderPayload(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		raw      string
		wantBody string
	}{
		{name: "outer response", raw: `[]`, wantBody: `[]`},
		{name: "missing thread", raw: `{}`},
		{name: "thread", raw: `{"thread":[]}`, wantBody: `[]`},
		{name: "turn", raw: `{"thread":{"id":"test-id","turns":[[]]}}`, wantBody: `[]`},
		{
			name:     "item",
			raw:      `{"thread":{"id":"test-id","turns":[{"id":"turn-id","items":[{"type":1}]}]}}`,
			wantBody: `{"type":1}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			send := func(context.Context, string, any) (jsonrpc.Response, error) {
				return jsonrpc.Response{Result: json.RawMessage(tc.raw)}, nil
			}
			read, err := ReadThread(t.Context(), send, "test-id", nil)
			var history *types.ThreadHistoryError
			if read != nil || !errors.As(err, &history) || history.Code != types.ThreadHistoryMalformed || history.ThreadID != "test-id" {
				t.Fatalf("read = %+v, err = %#v, want malformed history for test-id", read, err)
			}
			if tc.wantBody == "" {
				if history.Unwrap() != nil {
					t.Fatalf("missing thread cause = %#v, want nil", history.Unwrap())
				}
				return
			}
			var decode *types.JSONDecodeError
			var typeMismatch *json.UnmarshalTypeError
			if !errors.As(err, &decode) || decode.Raw != tc.wantBody || !errors.As(err, &typeMismatch) {
				t.Fatalf("malformed history cause = %#v, want type error for %s", history.Unwrap(), tc.wantBody)
			}
		})
	}
}

func TestHistoryDecodesNativeTimestampsAndTaggedStatus(t *testing.T) {
	t.Parallel()

	const wantCreated = "2023-11-14T22:13:20Z"
	const wantStarted = "2023-11-14T22:13:21Z"
	const legacyUpdated = "2023-11-14T22:13:22Z"
	raw := json.RawMessage(`{
		"id":"thread","createdAt":1700000000,"updatedAt":"2023-11-14T22:13:22Z",
		"status":{"type":"active","activeFlags":[]},
		"turns":[{"id":"turn","status":"completed","startedAt":1700000001,"completedAt":null,"items":[]}]
	}`)
	record, err := parseThreadRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	if record.CreatedAt != wantCreated || record.UpdatedAt != legacyUpdated || record.Status != "active" {
		t.Fatalf("metadata = %+v, want UTC creation, unchanged update and active status", record)
	}
	if len(record.Turns) != 1 || record.Turns[0].StartedAt != wantStarted ||
		record.Turns[0].CompletedAt != "" || record.Turns[0].Status != "completed" {
		t.Fatalf("turns = %+v, want UTC start, missing completion and unchanged status", record.Turns)
	}

	info, err := parseThreadInfo(json.RawMessage(`{"id":"thread","updatedAt":1700000000}`))
	if err != nil || info.LastModified != wantCreated {
		t.Fatalf("thread info = %+v, err = %v, want UTC last modification", info, err)
	}
}

func TestHistoryRejectsMalformedTimestampOrStatus(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"id":"thread","createdAt":1.5}`,
		`{"id":"thread","createdAt":true}`,
		`{"id":"thread","createdAt":9223372036854775807}`,
		`{"id":"thread","status":{"activeFlags":[]}}`,
	} {
		_, err := parseThreadRecord(json.RawMessage(payload))
		var decode *types.JSONDecodeError
		if !errors.As(err, &decode) {
			t.Errorf("parseThreadRecord(%s) = %v, want typed JSON decode error", payload, err)
		}
	}
}
