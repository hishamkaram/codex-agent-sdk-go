package events

import (
	"encoding/json"
	"os"
	"path/filepath"
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
			// Feature 187 US2: FileChange reshape per v2 schema. Legacy flat
			// Path/Operation/Diff fields are replaced by Changes[]FileChangePart.
			// Fixture-compensation per .claude/rules/fixture-compensation.md
			// (test-only, minimum edit to keep FR-016 satisfied under new invariant).
			name:     "fileChange",
			raw:      `{"type":"fileChange","id":"fc_1","status":"success","changes":[{"path":"/a.go","operation":"modify","diff":"---"}]}`,
			wantType: "fileChange",
			check: func(t *testing.T, item types.ThreadItem) {
				f := item.(*types.FileChange)
				if len(f.Changes) != 1 {
					t.Fatalf("len(Changes) = %d, want 1: %+v", len(f.Changes), f)
				}
				if f.Changes[0].Path != "/a.go" || f.Changes[0].Operation != "modify" || f.Changes[0].Diff != "---" {
					t.Fatalf("%+v", f.Changes[0])
				}
			},
		},
		{
			// Feature 187 US1: wire tags corrected from serverName/toolName/input
			// to server/tool/arguments per v2 schema. Fixture-compensation edit
			// per .claude/rules/fixture-compensation.md (test-only, minimum edit).
			name: "mcpToolCall",
			raw: `{"type":"mcpToolCall","server":"docs","tool":"search",` +
				`"arguments":{"q":"foo"},"result":{"hits":3},"status":"success"}`,
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
			// Feature 187 US2: WebSearch.Results was a fabricated field not in
			// v2 schema. Replaced with Action json.RawMessage matching schema.
			// Fixture-compensation per .claude/rules/fixture-compensation.md
			// (test-only, minimum edit to keep FR-016 satisfied under new invariant).
			name:     "webSearch",
			raw:      `{"type":"webSearch","query":"go maps","action":{"kind":"search"}}`,
			wantType: "webSearch",
			check: func(t *testing.T, item types.ThreadItem) {
				w := item.(*types.WebSearch)
				if w.Query != "go maps" {
					t.Fatalf("Query = %q; want %q", w.Query, "go maps")
				}
				if len(w.Action) == 0 {
					t.Fatalf("Action empty; want non-empty RawMessage")
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
			// Feature 187 US2: Plan.Content renamed to Text per v2 schema
			// (JSON tag was "content", real codex sends "text").
			// Fixture-compensation per .claude/rules/fixture-compensation.md
			// (test-only, minimum edit to keep FR-016 satisfied under new invariant).
			name:     "plan",
			raw:      `{"type":"plan","text":"1. step","status":"active"}`,
			wantType: "plan",
			check: func(t *testing.T, item types.ThreadItem) {
				p := item.(*types.Plan)
				if p.Text != "1. step" || p.Status != "active" {
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

// TestParseItem_CommandExecution_WithStringProcessID pins the US1-AC1 behavior:
// codex 0.121.0 sends `processId` as a string (not an int). The legacy SDK
// struct tagged it as `*int`, causing MessageParseError on every real
// commandExecution notification. This test seeds the verbatim payload from
// the user's 2026-04-20 daemon log and asserts clean parse with ProcessID
// populated as a string.
func TestParseItem_CommandExecution_WithStringProcessID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		wantProcessID string
	}{
		{
			name: "user log payload — processId as string",
			raw: `{"type":"commandExecution","id":"call_EGzRi6xUDSf89AQIg3UyhtFk",` +
				`"command":"/bin/bash -lc \"sed -n '1,220p' /home/hesham/.codex/skills/.system/openai-docs/SKILL.md\"",` +
				`"cwd":"/home/hesham/Active-Projects/push-test","processId":"12345"}`,
			wantProcessID: "12345",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			it, err := ParseItem(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("ParseItem returned error: %v", err)
			}
			c, ok := it.(*types.CommandExecution)
			if !ok {
				t.Fatalf("ParseItem returned %T, want *types.CommandExecution", it)
			}
			if c.ProcessID != tt.wantProcessID {
				t.Fatalf("ProcessID = %q, want %q", c.ProcessID, tt.wantProcessID)
			}
		})
	}
}

// TestParseItem_MCPToolCall_WithObjectError pins US1-AC2 behavior: codex
// 0.121.0 sends MCPToolCall.error as an object `{"message":"..."}` and uses
// camelCase tags `server`, `tool`, `arguments`. Legacy SDK had tags
// `serverName`, `toolName`, `input` and `ErrorText string`, which silently
// dropped all three fields AND errored on the object-shaped error.
func TestParseItem_MCPToolCall_WithObjectError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		raw            string
		wantServer     string
		wantTool       string
		wantStatus     string
		wantErrorText  string
		wantInputBytes bool
	}{
		{
			name: "user log payload — object error + camelCase tags",
			raw: `{"type":"mcpToolCall","id":"call_c8KztAWsOupsIOUESsLkWCXD",` +
				`"server":"openaiDeveloperDocs","tool":"list_mcp_resource_templates",` +
				`"status":"failed","arguments":{"server":"openaiDeveloperDocs"},` +
				`"result":null,"error":{"message":"not supported"}}`,
			wantServer:     "openaiDeveloperDocs",
			wantTool:       "list_mcp_resource_templates",
			wantStatus:     "failed",
			wantErrorText:  "not supported",
			wantInputBytes: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			it, err := ParseItem(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("ParseItem returned error: %v", err)
			}
			m, ok := it.(*types.MCPToolCall)
			if !ok {
				t.Fatalf("ParseItem returned %T, want *types.MCPToolCall", it)
			}
			if m.ServerName != tt.wantServer {
				t.Errorf("ServerName = %q, want %q", m.ServerName, tt.wantServer)
			}
			if m.ToolName != tt.wantTool {
				t.Errorf("ToolName = %q, want %q", m.ToolName, tt.wantTool)
			}
			if m.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", m.Status, tt.wantStatus)
			}
			if tt.wantInputBytes && len(m.Input) == 0 {
				t.Errorf("Input is empty; want non-empty RawMessage (arguments object)")
			}
			if got := m.ErrorText(); got != tt.wantErrorText {
				t.Errorf("ErrorText() = %q, want %q", got, tt.wantErrorText)
			}
		})
	}
}

// TestParseItem_FileChange_ChangesArray pins US2-AC1 behavior: codex v2 schema
// defines fileChange with a nested changes[] array of FileChangePart (path +
// operation + diff). The legacy SDK shape (flat Path/Operation/Diff) silently
// dropped data for any real codex fileChange payload. This test asserts the
// new shape parses correctly and exposes all per-part fields.
func TestParseItem_FileChange_ChangesArray(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(
		`{"type":"fileChange","id":"fc1","status":"completed",` +
			`"changes":[` +
			`{"path":"/tmp/a","operation":"modify","diff":"---\n+++"},` +
			`{"path":"/tmp/b","operation":"create","diff":""}` +
			`]}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	f, ok := it.(*types.FileChange)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.FileChange", it)
	}
	if len(f.Changes) != 2 {
		t.Fatalf("len(Changes) = %d, want 2: %+v", len(f.Changes), f.Changes)
	}
	if f.Changes[0].Path != "/tmp/a" {
		t.Errorf("Changes[0].Path = %q, want %q", f.Changes[0].Path, "/tmp/a")
	}
	if f.Changes[0].Operation != "modify" {
		t.Errorf("Changes[0].Operation = %q, want %q", f.Changes[0].Operation, "modify")
	}
	if f.Changes[1].Path != "/tmp/b" {
		t.Errorf("Changes[1].Path = %q, want %q", f.Changes[1].Path, "/tmp/b")
	}
	if f.Changes[1].Operation != "create" {
		t.Errorf("Changes[1].Operation = %q, want %q", f.Changes[1].Operation, "create")
	}
}

// TestParseItem_WebSearch_ActionField pins US2-AC2 behavior: codex v2 schema
// defines webSearch with a raw `action` JSON value (not a `results` array —
// the latter was a fabrication in the legacy SDK). This test asserts Query
// still parses and Action is populated as a non-empty RawMessage.
func TestParseItem_WebSearch_ActionField(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(
		`{"type":"webSearch","id":"ws1","query":"go maps",` +
			`"action":{"kind":"search","params":{"q":"go maps"}}}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	w, ok := it.(*types.WebSearch)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.WebSearch", it)
	}
	if w.Query != "go maps" {
		t.Errorf("Query = %q, want %q", w.Query, "go maps")
	}
	if len(w.Action) == 0 {
		t.Errorf("Action is empty; want non-empty RawMessage")
	}
}

// TestParseItem_Plan_TextField pins US2-AC3 behavior: codex v2 schema tags
// the plan body as `text` (not `content` — the legacy tag). This test asserts
// the new tag parses correctly onto the Text field.
func TestParseItem_Plan_TextField(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"plan","id":"p1","text":"step 1\nstep 2\nstep 3"}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	p, ok := it.(*types.Plan)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.Plan", it)
	}
	if p.Text != "step 1\nstep 2\nstep 3" {
		t.Errorf("Text = %q, want %q", p.Text, "step 1\nstep 2\nstep 3")
	}
}

// -----------------------------------------------------------------------------
// Feature 187 US3: new 0.121.0 item types
// -----------------------------------------------------------------------------

// TestParseItem_HookPrompt (T030-T) pins US3-AC2: codex 0.121.0 emits
// hookPrompt items that today fall through to UnknownItem. After US3, the
// parser MUST dispatch hookPrompt to a concrete *types.HookPrompt with a
// Fragments slice of raw JSON fragments.
func TestParseItem_HookPrompt(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"hookPrompt","id":"hp1","fragments":[{"a":1},{"b":2}]}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	hp, ok := it.(*types.HookPrompt)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.HookPrompt", it)
	}
	if hp.ID != "hp1" {
		t.Errorf("ID = %q, want %q", hp.ID, "hp1")
	}
	if len(hp.Fragments) != 2 {
		t.Errorf("len(Fragments) = %d, want 2", len(hp.Fragments))
	}
}

// TestParseItem_DynamicToolCall (T031-T) pins US3-AC3: codex 0.121.0 emits
// dynamicToolCall items used for non-MCP, non-builtin tool invocations.
func TestParseItem_DynamicToolCall(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(
		`{"type":"dynamicToolCall","id":"dt1","tool":"web_fetch",` +
			`"arguments":{"url":"https://x"},"status":"inProgress","contentItems":[]}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	dt, ok := it.(*types.DynamicToolCall)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.DynamicToolCall", it)
	}
	if dt.ID != "dt1" {
		t.Errorf("ID = %q, want %q", dt.ID, "dt1")
	}
	if dt.Tool != "web_fetch" {
		t.Errorf("Tool = %q, want %q", dt.Tool, "web_fetch")
	}
	if dt.Status != "inProgress" {
		t.Errorf("Status = %q, want %q", dt.Status, "inProgress")
	}
	if len(dt.Arguments) == 0 {
		t.Errorf("Arguments is empty; want non-empty RawMessage")
	}
}

// TestParseItem_CollabAgentToolCall (T032-T) pins US3-AC1: codex 0.121.0 emits
// collabAgentToolCall items for delegated sub-agents. This is what populates
// the PWA agent panel. After US3, the parser MUST dispatch to a concrete
// *types.CollabAgentToolCall with AgentsStates, SenderThreadID,
// ReceiverThreadIDs, Model, Prompt, and ReasoningEffort populated.
func TestParseItem_CollabAgentToolCall(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(
		`{"type":"collabAgentToolCall","id":"ct1","tool":"Task","status":"inProgress",` +
			`"agentsStates":[{"state":"running"},{"state":"idle"}],` +
			`"senderThreadId":"s1","receiverThreadIds":["r1","r2"],` +
			`"model":"gpt-5","prompt":"research X","reasoningEffort":"high"}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	ct, ok := it.(*types.CollabAgentToolCall)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.CollabAgentToolCall", it)
	}
	if ct.ID != "ct1" {
		t.Errorf("ID = %q, want %q", ct.ID, "ct1")
	}
	if ct.Tool != "Task" {
		t.Errorf("Tool = %q, want %q", ct.Tool, "Task")
	}
	if ct.Status != "inProgress" {
		t.Errorf("Status = %q, want %q", ct.Status, "inProgress")
	}
	if ct.SenderThreadID != "s1" {
		t.Errorf("SenderThreadID = %q, want %q", ct.SenderThreadID, "s1")
	}
	if ct.Model != "gpt-5" {
		t.Errorf("Model = %q, want %q", ct.Model, "gpt-5")
	}
	if len(ct.AgentsStates) != 2 {
		t.Errorf("len(AgentsStates) = %d, want 2", len(ct.AgentsStates))
	}
	if len(ct.ReceiverThreadIDs) != 2 {
		t.Errorf("len(ReceiverThreadIDs) = %d, want 2", len(ct.ReceiverThreadIDs))
	}
}

// TestParseItem_ImageView (T033-T part 1) pins US3-AC3: codex 0.121.0 emits
// imageView items when displaying an inline image. After US3, the parser
// MUST dispatch to a concrete *types.ImageView with Path populated.
func TestParseItem_ImageView(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"imageView","id":"iv1","path":"/tmp/a.png"}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	iv, ok := it.(*types.ImageView)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.ImageView", it)
	}
	if iv.ID != "iv1" {
		t.Errorf("ID = %q, want %q", iv.ID, "iv1")
	}
	if iv.Path != "/tmp/a.png" {
		t.Errorf("Path = %q, want %q", iv.Path, "/tmp/a.png")
	}
}

// TestParseItem_ImageGeneration (T033-T part 2) pins US3-AC3: codex 0.121.0
// emits imageGeneration items for DALL-E style generations. After US3, the
// parser MUST dispatch to a concrete *types.ImageGeneration.
func TestParseItem_ImageGeneration(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(
		`{"type":"imageGeneration","id":"ig1","status":"completed",` +
			`"savedPath":"/tmp/out.png","revisedPrompt":"a cat"}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	ig, ok := it.(*types.ImageGeneration)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.ImageGeneration", it)
	}
	if ig.ID != "ig1" {
		t.Errorf("ID = %q, want %q", ig.ID, "ig1")
	}
	if ig.Status != "completed" {
		t.Errorf("Status = %q, want %q", ig.Status, "completed")
	}
	if ig.SavedPath != "/tmp/out.png" {
		t.Errorf("SavedPath = %q, want %q", ig.SavedPath, "/tmp/out.png")
	}
	if ig.RevisedPrompt != "a cat" {
		t.Errorf("RevisedPrompt = %q, want %q", ig.RevisedPrompt, "a cat")
	}
}

// TestParseItem_EnteredReviewMode (T034-T part 1) pins US3-AC3: codex 0.121.0
// emits enteredReviewMode items when the agent enters a review flow. The
// Review field carries arbitrary JSON (schema is loose here).
func TestParseItem_EnteredReviewMode(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"enteredReviewMode","id":"er1","review":{"mode":"diff"}}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	er, ok := it.(*types.EnteredReviewMode)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.EnteredReviewMode", it)
	}
	if er.ID != "er1" {
		t.Errorf("ID = %q, want %q", er.ID, "er1")
	}
	if len(er.Review) == 0 {
		t.Errorf("Review is empty; want non-empty RawMessage")
	}
}

// TestParseItem_ExitedReviewMode (T034-T part 2) pins US3-AC3: codex 0.121.0
// emits exitedReviewMode items when the agent exits a review flow.
func TestParseItem_ExitedReviewMode(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"exitedReviewMode","id":"ex1","review":{"mode":"diff"}}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	ex, ok := it.(*types.ExitedReviewMode)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.ExitedReviewMode", it)
	}
	if ex.ID != "ex1" {
		t.Errorf("ID = %q, want %q", ex.ID, "ex1")
	}
	if len(ex.Review) == 0 {
		t.Errorf("Review is empty; want non-empty RawMessage")
	}
}

// TestParseItem_ContextCompaction (T034-T part 3) pins US3-AC3: codex 0.121.0
// emits contextCompaction items when compacting the conversation context.
// The struct is minimal (id only per schema).
func TestParseItem_ContextCompaction(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"contextCompaction","id":"cc1"}`)

	it, err := ParseItem(raw)
	if err != nil {
		t.Fatalf("ParseItem returned error: %v", err)
	}
	cc, ok := it.(*types.ContextCompaction)
	if !ok {
		t.Fatalf("ParseItem returned %T, want *types.ContextCompaction", it)
	}
	if cc.ID != "cc1" {
		t.Errorf("ID = %q, want %q", cc.ID, "cc1")
	}
}

// TestParseItem_Switch_CoversAllSchemaVariants (T035-T) pins US3-AC4: the
// ParseItem switch MUST dispatch every ThreadItem discriminator defined in
// the vendored v2 schema to a concrete typed item — NEVER falling through to
// *types.UnknownItem for any known schema variant.
//
// Strategy: load the schema, iterate ThreadItem.oneOf, synthesize a minimal
// payload for each discriminator with the required stringy fields (via a
// hardcoded per-discriminator fallback map), and assert the parser returns
// a non-UnknownItem. Discriminators not in the fallback map are logged and
// skipped (so a future codex release adding a new type does not fail this
// test until the fallback map is extended).
//
// This test COMPILES without the new types existing but runtime-fails for
// any discriminator that dispatches to UnknownItem — which includes the 7
// new 0.121.0 types until US3 Pass B adds the dispatch cases.
func TestParseItem_Switch_CoversAllSchemaVariants(t *testing.T) {
	t.Parallel()

	// Per-discriminator minimal payload. Includes any required non-type,
	// non-id stringy/array/object fields from the schema so the outer
	// json.Unmarshal does not choke on missing required fields. The key is
	// the wire discriminator (as emitted by codex), the value is the
	// minimal JSON payload.
	minimalPayloads := map[string]string{
		"agentMessage":        `{"type":"agentMessage","id":"test","text":""}`,
		"userMessage":         `{"type":"userMessage","id":"test","content":[]}`,
		"commandExecution":    `{"type":"commandExecution","id":"test","command":"x","cwd":"/","status":"inProgress","commandActions":[]}`,
		"fileChange":          `{"type":"fileChange","id":"test","status":"inProgress","changes":[]}`,
		"mcpToolCall":         `{"type":"mcpToolCall","id":"test","server":"s","tool":"t","arguments":{},"status":"inProgress"}`,
		"webSearch":           `{"type":"webSearch","id":"test","query":"q"}`,
		"memoryRead":          `{"type":"memoryRead","id":"test"}`,
		"memoryWrite":         `{"type":"memoryWrite","id":"test"}`,
		"plan":                `{"type":"plan","id":"test","text":""}`,
		"reasoning":           `{"type":"reasoning","id":"test"}`,
		"systemError":         `{"type":"systemError","id":"test","message":""}`,
		"hookPrompt":          `{"type":"hookPrompt","id":"test","fragments":[]}`,
		"dynamicToolCall":     `{"type":"dynamicToolCall","id":"test","tool":"t","arguments":{},"status":"inProgress"}`,
		"collabAgentToolCall": `{"type":"collabAgentToolCall","id":"test","tool":"t","status":"inProgress","agentsStates":[],"senderThreadId":"s","receiverThreadIds":[]}`,
		"imageView":           `{"type":"imageView","id":"test","path":"/x"}`,
		"imageGeneration":     `{"type":"imageGeneration","id":"test","status":"completed","result":{}}`,
		"enteredReviewMode":   `{"type":"enteredReviewMode","id":"test","review":{}}`,
		"exitedReviewMode":    `{"type":"exitedReviewMode","id":"test","review":{}}`,
		"contextCompaction":   `{"type":"contextCompaction","id":"test"}`,
	}

	// Load the schema and enumerate ThreadItem.oneOf discriminators.
	schemaPath := filepath.Join("testdata", "schema", "codex_app_server_protocol.v2.schemas.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	threadItemRaw, ok := doc.Definitions["ThreadItem"]
	if !ok {
		t.Fatalf("schema missing definitions.ThreadItem")
	}
	var threadItem struct {
		OneOf []struct {
			Title      string `json:"title"`
			Properties struct {
				Type struct {
					Enum []string `json:"enum"`
				} `json:"type"`
			} `json:"properties"`
		} `json:"oneOf"`
	}
	if err := json.Unmarshal(threadItemRaw, &threadItem); err != nil {
		t.Fatalf("unmarshal ThreadItem: %v", err)
	}
	if len(threadItem.OneOf) == 0 {
		t.Fatalf("ThreadItem.oneOf is empty")
	}

	for _, variant := range threadItem.OneOf {
		variant := variant
		if len(variant.Properties.Type.Enum) == 0 {
			t.Logf("skipping %s — no type.enum in schema", variant.Title)
			continue
		}
		discriminator := variant.Properties.Type.Enum[0]
		payload, ok := minimalPayloads[discriminator]
		if !ok {
			t.Logf("skipping %q — no minimal payload fallback (extend minimalPayloads to cover this variant)", discriminator)
			continue
		}
		t.Run(discriminator, func(t *testing.T) {
			t.Parallel()
			it, err := ParseItem(json.RawMessage(payload))
			if err != nil {
				t.Fatalf("ParseItem(%q) returned error: %v", discriminator, err)
			}
			if _, isUnknown := it.(*types.UnknownItem); isUnknown {
				t.Errorf("variant %q dispatched to UnknownItem; expected concrete type", discriminator)
			}
		})
	}
}
