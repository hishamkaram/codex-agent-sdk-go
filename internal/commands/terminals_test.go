package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestListBackgroundTerminalsRejectsInvalidContinuationRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		reply          string
		message        string
		wantPagination bool
		wantDecode     bool
	}{
		{
			name:           "same process on different pages",
			reply:          `{"data":[{"processId":"process-1","itemId":"item-2"}]}`,
			message:        `duplicate process "process-1"`,
			wantPagination: true,
		},
		{
			name:    "missing item identity",
			reply:   `{"data":[{"processId":"process-2","itemId":" "}]}`,
			message: "malformed terminal row",
		},
		{
			name:       "invalid process type",
			reply:      `{"data":[{"processId":42,"itemId":"item-2"}]}`,
			message:    "decode response",
			wantDecode: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			paginationErr := errors.New("test pagination error")
			pages := 0
			send := func(_ context.Context, callerName, method string, params any) (json.RawMessage, error) {
				if callerName != "ListBackgroundTerminals" || method != "thread/backgroundTerminals/list" {
					t.Fatalf("caller/method = %q/%q", callerName, method)
				}
				wire, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var query struct {
					ThreadID string  `json:"threadId"`
					Limit    uint32  `json:"limit"`
					Cursor   *string `json:"cursor"`
				}
				if err := json.Unmarshal(wire, &query); err != nil {
					t.Fatal(err)
				}
				if query.ThreadID != "thread-1" || query.Limit != 100 {
					t.Fatalf("incorrect page payload: %s", wire)
				}
				pages++
				switch pages {
				case 1:
					if query.Cursor != nil {
						t.Fatalf("first page has cursor: %s", wire)
					}
					return json.RawMessage(`{"data":[{"processId":"process-1","itemId":"item-1"}],"nextCursor":"continue"}`), nil
				case 2:
					if query.Cursor == nil || *query.Cursor != "continue" {
						t.Fatalf("incorrect continuation: %s", wire)
					}
					return json.RawMessage(tt.reply), nil
				default:
					t.Fatal("request sent after invalid continuation")
					return nil, nil
				}
			}

			rows, err := ListBackgroundTerminals(t.Context(), send, "thread-1", paginationErr)
			if pages != 2 || rows != nil || err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("pages = %d, rows = %v, err = %v. Want two pages, no partial rows, and %q", pages, rows, err, tt.message)
			}
			if errors.Is(err, paginationErr) != tt.wantPagination || types.IsJSONDecodeError(err) != tt.wantDecode {
				t.Fatalf("unexpected error identity: %v", err)
			}
		})
	}
}

func TestCleanBackgroundTerminalsSendsExactThreadAndPreservesResult(t *testing.T) {
	t.Parallel()
	rejected := errors.New("terminal cleanup rejected")
	for _, tc := range []struct {
		name       string
		threadID   string
		replyError error
	}{
		{"blank thread", "  ", nil},
		{"successful cleanup", "thread:remote/1", nil},
		{"provider rejection", "thread:remote/1", rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			send := func(_ context.Context, caller, method string, params any) (json.RawMessage, error) {
				calls++
				wire, err := json.Marshal(params)
				if err != nil || caller != "CleanBackgroundTerminals" || method != "thread/backgroundTerminals/clean" ||
					string(wire) != `{"threadId":"thread:remote/1"}` {
					t.Fatalf("request = %q / %q / %s, err = %v", caller, method, wire, err)
				}
				return json.RawMessage(`{}`), tc.replyError
			}
			err := CleanBackgroundTerminals(t.Context(), send, tc.threadID)
			if tc.threadID == "  " {
				if err == nil || !strings.Contains(err.Error(), "threadID is required") || calls != 0 {
					t.Fatalf("blank thread: calls = %d, err = %v", calls, err)
				}
				return
			}
			if calls != 1 || !errors.Is(err, tc.replyError) {
				t.Fatalf("calls = %d, err = %v; want one call and %v", calls, err, tc.replyError)
			}
		})
	}
}
