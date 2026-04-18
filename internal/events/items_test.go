package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestParseItem_KnownSubtypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		wantType string
		check    func(t *testing.T, item types.ThreadItem)
	}{
		{
			name:     "agent_message",
			raw:      `{"type":"agent_message","content":"Hello"}`,
			wantType: "agent_message",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.AgentMessage)
				if m.Content != "Hello" {
					t.Fatalf("content = %q", m.Content)
				}
			},
		},
		{
			name:     "user_message",
			raw:      `{"type":"user_message","content":"Hi"}`,
			wantType: "user_message",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.UserMessage)
				if m.Content != "Hi" {
					t.Fatalf("content = %q", m.Content)
				}
			},
		},
		{
			name: "command_execution",
			raw: `{"type":"command_execution","command":"ls","working_directory":"/tmp",` +
				`"exit_code":0,"stdout":"file1\n","stderr":"","status":"success"}`,
			wantType: "command_execution",
			check: func(t *testing.T, item types.ThreadItem) {
				c := item.(*types.CommandExecution)
				if c.Command != "ls" || c.Cwd != "/tmp" || c.ExitCode != 0 || c.Status != "success" {
					t.Fatalf("unexpected: %+v", c)
				}
				if c.Stdout != "file1\n" {
					t.Fatalf("stdout: %q", c.Stdout)
				}
			},
		},
		{
			name:     "file_change",
			raw:      `{"type":"file_change","path":"/a.go","operation":"modify","diff":"---","status":"success"}`,
			wantType: "file_change",
			check: func(t *testing.T, item types.ThreadItem) {
				f := item.(*types.FileChange)
				if f.Path != "/a.go" || f.Operation != "modify" || f.Diff != "---" {
					t.Fatalf("%+v", f)
				}
			},
		},
		{
			name: "mcp_tool_call",
			raw: `{"type":"mcp_tool_call","server_name":"docs","tool_name":"search",` +
				`"input":{"q":"foo"},"result":{"hits":3},"status":"success"}`,
			wantType: "mcp_tool_call",
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
			name:     "web_search",
			raw:      `{"type":"web_search","query":"go maps","results":[{"title":"T","url":"U","snippet":"S"}]}`,
			wantType: "web_search",
			check: func(t *testing.T, item types.ThreadItem) {
				w := item.(*types.WebSearch)
				if w.Query != "go maps" || len(w.Results) != 1 || w.Results[0].Title != "T" {
					t.Fatalf("%+v", w)
				}
			},
		},
		{
			name:     "memory_read",
			raw:      `{"type":"memory_read","query":"k","result":"v"}`,
			wantType: "memory_read",
			check: func(t *testing.T, item types.ThreadItem) {
				m := item.(*types.MemoryRead)
				if m.Query != "k" || m.Result != "v" {
					t.Fatalf("%+v", m)
				}
			},
		},
		{
			name:     "memory_write",
			raw:      `{"type":"memory_write","key":"k","value":"v"}`,
			wantType: "memory_write",
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
	raw := json.RawMessage(`{"type":"future_subtype","brand_new_field":"x"}`)
	it, err := ParseItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := it.(*types.UnknownItem)
	if !ok {
		t.Fatalf("got %T, want *UnknownItem", it)
	}
	if u.Type != "future_subtype" {
		t.Fatalf("Type = %q", u.Type)
	}
	if string(u.Raw) != string(raw) {
		t.Fatalf("Raw not preserved: got %q, want %q", u.Raw, raw)
	}
	if u.ItemType() != "future_subtype" {
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

func TestParseItemDelta_KnownDeltas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		wantType string
		check    func(t *testing.T, d types.ItemDelta)
	}{
		{
			name:     "agent_message_delta",
			raw:      `{"type":"agent_message_delta","text_chunk":"Hel"}`,
			wantType: "agent_message_delta",
			check: func(t *testing.T, d types.ItemDelta) {
				x := d.(*types.AgentMessageDelta)
				if x.TextChunk != "Hel" {
					t.Fatal(x.TextChunk)
				}
			},
		},
		{
			name:     "reasoning_delta",
			raw:      `{"type":"reasoning_delta","text_chunk":"ttt","summary_chunk":"sss"}`,
			wantType: "reasoning_delta",
			check: func(t *testing.T, d types.ItemDelta) {
				x := d.(*types.ReasoningDelta)
				if x.TextChunk != "ttt" || x.SummaryChunk != "sss" {
					t.Fatalf("%+v", x)
				}
			},
		},
		{
			name:     "command_output_delta",
			raw:      `{"type":"command_output_delta","stdout_chunk":"A","stderr_chunk":"B"}`,
			wantType: "command_output_delta",
			check: func(t *testing.T, d types.ItemDelta) {
				x := d.(*types.CommandOutputDelta)
				if x.StdoutChunk != "A" || x.StderrChunk != "B" {
					t.Fatalf("%+v", x)
				}
			},
		},
		{
			name:     "file_change_output_delta",
			raw:      `{"type":"file_change_output_delta","diff_chunk":"+line"}`,
			wantType: "file_change_output_delta",
			check: func(t *testing.T, d types.ItemDelta) {
				x := d.(*types.FileChangeOutputDelta)
				if x.DiffChunk != "+line" {
					t.Fatal(x.DiffChunk)
				}
			},
		},
		{
			name:     "mcp_tool_call_progress",
			raw:      `{"type":"mcp_tool_call_progress","stage":"running","progress":42}`,
			wantType: "mcp_tool_call_progress",
			check: func(t *testing.T, d types.ItemDelta) {
				x := d.(*types.MCPToolCallProgress)
				if x.Stage != "running" || x.Progress != 42 {
					t.Fatalf("%+v", x)
				}
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := ParseItemDelta(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if d.DeltaType() != tt.wantType {
				t.Fatalf("DeltaType() = %q, want %q", d.DeltaType(), tt.wantType)
			}
			tt.check(t, d)
		})
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
