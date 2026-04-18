package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Wire shapes verified against CLI 0.121.0 transcript captures. All
// discriminators camelCase; field names per ground truth.
func TestParseItem_KnownSubtypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		wantType string
		check    func(t *testing.T, item types.ThreadItem)
	}{
		{
			name:     "agentMessage",
			raw:      `{"type":"agentMessage","id":"msg_1","text":"Hello","phase":"final_answer"}`,
			wantType: "agentMessage",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.AgentMessage)
				if m.ID != "msg_1" || m.Text != "Hello" || m.Phase != "final_answer" {
					t.Fatalf("%+v", m)
				}
			},
		},
		{
			name:     "userMessage",
			raw:      `{"type":"userMessage","id":"u_1","content":[{"type":"text","text":"Hi"}]}`,
			wantType: "userMessage",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.UserMessage)
				if m.ID != "u_1" || len(m.Content) != 1 {
					t.Fatalf("%+v", m)
				}
				if m.Content[0].Type != "text" || m.Content[0].Text != "Hi" {
					t.Fatalf("part: %+v", m.Content[0])
				}
			},
		},
		{
			name: "commandExecution",
			raw: `{"type":"commandExecution","id":"call_1","command":"/bin/bash -lc ls",` +
				`"cwd":"/tmp","source":"agent","status":"success","exitCode":0,` +
				`"aggregatedOutput":"file1\n","durationMs":42}`,
			wantType: "commandExecution",
			check: func(t *testing.T, item types.ThreadItem) {
				c := item.(*types.CommandExecution)
				if c.Command != "/bin/bash -lc ls" || c.Cwd != "/tmp" || c.Source != "agent" {
					t.Fatalf("%+v", c)
				}
				if c.Status != "success" || c.AggregatedOutput != "file1\n" {
					t.Fatalf("%+v", c)
				}
				if c.ExitCode == nil || *c.ExitCode != 0 {
					t.Fatalf("ExitCode: %v", c.ExitCode)
				}
				if c.DurationMs == nil || *c.DurationMs != 42 {
					t.Fatalf("DurationMs: %v", c.DurationMs)
				}
			},
		},
		{
			name:     "fileChange",
			raw:      `{"type":"fileChange","path":"/a.go","operation":"modify","diff":"---","status":"success"}`,
			wantType: "fileChange",
			check: func(t *testing.T, item types.ThreadItem) {
				f := item.(*types.FileChange)
				if f.Path != "/a.go" || f.Operation != "modify" || f.Diff != "---" {
					t.Fatalf("%+v", f)
				}
			},
		},
		{
			name: "mcpToolCall",
			raw: `{"type":"mcpToolCall","serverName":"docs","toolName":"search",` +
				`"input":{"q":"foo"},"result":{"hits":3},"status":"success"}`,
			wantType: "mcpToolCall",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.MCPToolCall)
				if m.ServerName != "docs" || m.ToolName != "search" {
					t.Fatalf("%+v", m)
				}
				if string(m.Input) != `{"q":"foo"}` {
					t.Fatalf("input: %q", m.Input)
				}
			},
		},
		{
			name:     "webSearch",
			raw:      `{"type":"webSearch","query":"go maps","results":[{"title":"T","url":"U","snippet":"S"}]}`,
			wantType: "webSearch",
			check: func(t *testing.T, item types.ThreadItem) {
				w := item.(*types.WebSearch)
				if w.Query != "go maps" || len(w.Results) != 1 || w.Results[0].Title != "T" {
					t.Fatalf("%+v", w)
				}
			},
		},
		{
			name:     "memoryRead",
			raw:      `{"type":"memoryRead","query":"k","result":"v"}`,
			wantType: "memoryRead",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.MemoryRead)
				if m.Query != "k" || m.Result != "v" {
					t.Fatalf("%+v", m)
				}
			},
		},
		{
			name:     "memoryWrite",
			raw:      `{"type":"memoryWrite","key":"k","value":"v"}`,
			wantType: "memoryWrite",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.MemoryWrite)
				if m.Key != "k" || m.Value != "v" {
					t.Fatalf("%+v", m)
				}
			},
		},
		{
			name:     "plan",
			raw:      `{"type":"plan","content":"1. step","status":"active"}`,
			wantType: "plan",
			check: func(t *testing.T, item types.ThreadItem) {
				p := item.(*types.Plan)
				if p.Content != "1. step" || p.Status != "active" {
					t.Fatalf("%+v", p)
				}
			},
		},
		{
			name:     "reasoning",
			raw:      `{"type":"reasoning","id":"rs_1","summary":["s1","s2"],"content":["c1"]}`,
			wantType: "reasoning",
			check: func(t *testing.T, item types.ThreadItem) {
				r := item.(*types.Reasoning)
				if r.ID != "rs_1" || len(r.Summary) != 2 || len(r.Content) != 1 {
					t.Fatalf("%+v", r)
				}
			},
		},
		{
			name:     "reasoning_empty_arrays_from_item_started",
			raw:      `{"type":"reasoning","id":"rs_2","summary":[],"content":[]}`,
			wantType: "reasoning",
			check: func(t *testing.T, item types.ThreadItem) {
				r := item.(*types.Reasoning)
				if r.ID != "rs_2" || len(r.Summary) != 0 || len(r.Content) != 0 {
					t.Fatalf("%+v", r)
				}
			},
		},
		{
			name:     "systemError",
			raw:      `{"type":"systemError","id":"err_1","message":"model unavailable"}`,
			wantType: "systemError",
			check: func(t *testing.T, item types.ThreadItem) {
				s := item.(*types.SystemError)
				if s.ID != "err_1" || s.Message != "model unavailable" {
					t.Fatalf("%+v", s)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			it, err := ParseItem(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if it.ItemType() != tt.wantType {
				t.Fatalf("ItemType() = %q, want %q", it.ItemType(), tt.wantType)
			}
			tt.check(t, it)
		})
	}
}

func TestParseItem_UnknownTypeFallback(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"futureSubtype","brand_new_field":"x"}`)
	it, err := ParseItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := it.(*types.UnknownItem)
	if !ok {
		t.Fatalf("got %T, want *UnknownItem", it)
	}
	if u.Type != "futureSubtype" {
		t.Fatalf("Type = %q", u.Type)
	}
	if string(u.Raw) != string(raw) {
		t.Fatalf("Raw not preserved: got %q, want %q", u.Raw, raw)
	}
	if u.ItemType() != "futureSubtype" {
		t.Fatalf("ItemType() = %q", u.ItemType())
	}
}

func TestParseItem_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseItem(json.RawMessage(`{not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if !types.IsJSONDecodeError(err) {
		t.Fatalf("expected JSONDecodeError, got %T: %v", err, err)
	}
}

func TestParseItem_EmptyPayload(t *testing.T) {
	t.Parallel()
	_, err := ParseItem(json.RawMessage{})
	if err == nil {
		t.Fatal("expected error on empty payload")
	}
	if !types.IsMessageParseError(err) {
		t.Fatalf("expected MessageParseError, got %T", err)
	}
}

// TestParseItemDelta exercises the LEGACY ParseItemDelta path where a
// delta payload carries a {"type":"..."} discriminator. Real codex wire
// never uses this shape — the actual streaming methods (item/agentMessage
// /delta etc.) carry flat string deltas handled by parseFlatDelta /
// parseReasoningTextDelta / etc. in parser.go. ParseItemDelta is retained
// as a forward-compat hatch in case a future codex item/updated method
// uses discriminated deltas.
func TestParseItemDelta_LegacyAgentMessageShape(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"agent_message_delta","text_chunk":"Hel"}`)
	d, err := ParseItemDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeltaType() != "agentMessage/delta" {
		t.Fatalf("DeltaType() = %q", d.DeltaType())
	}
	x := d.(*types.AgentMessageDelta)
	if x.TextChunk != "Hel" {
		t.Fatalf("TextChunk = %q", x.TextChunk)
	}
}

func TestParseItemDelta_UnknownFallback(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"future_delta","foo":1}`)
	d, err := ParseItemDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := d.(*types.UnknownDelta)
	if !ok {
		t.Fatalf("got %T, want *UnknownDelta", d)
	}
	if u.Type != "future_delta" || u.DeltaType() != "future_delta" {
		t.Fatalf("%+v", u)
	}
}
