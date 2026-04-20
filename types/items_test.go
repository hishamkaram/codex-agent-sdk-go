package types

import (
	"encoding/json"
	"testing"
)

func TestItemType_EveryKnownItem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		item ThreadItem
		want string
	}{
		{&AgentMessage{}, "agentMessage"},
		{&UserMessage{}, "userMessage"},
		{&CommandExecution{}, "commandExecution"},
		{&FileChange{}, "fileChange"},
		{&MCPToolCall{}, "mcpToolCall"},
		{&WebSearch{}, "webSearch"},
		{&MemoryRead{}, "memoryRead"},
		{&MemoryWrite{}, "memoryWrite"},
		{&Plan{}, "plan"},
		{&Reasoning{}, "reasoning"},
		{&SystemError{}, "systemError"},
		{&UnknownItem{Type: "future"}, "future"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.item.ItemType(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDeltaType_EveryKnownDelta(t *testing.T) {
	t.Parallel()
	cases := []struct {
		delta ItemDelta
		want  string
	}{
		{&AgentMessageDelta{}, "agentMessage/delta"},
		{&ReasoningTextDelta{}, "reasoning/textDelta"},
		{&ReasoningSummaryTextDelta{}, "reasoning/summaryTextDelta"},
		{&ReasoningSummaryPartAdded{}, "reasoning/summaryPartAdded"},
		{&CommandOutputDelta{}, "commandExecution/outputDelta"},
		{&FileChangeOutputDelta{}, "fileChange/outputDelta"},
		{&PlanDelta{}, "plan/delta"},
		{&MCPToolCallProgress{}, "mcpToolCall/progress"},
		{&TerminalInteraction{}, "commandExecution/terminalInteraction"},
		{&UnknownDelta{Type: "future"}, "future"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.delta.DeltaType(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ptrInt64 returns a pointer to its argument — inline test helper for
// constructing pointer-typed optional schema fields.
func ptrInt64(v int64) *int64 { return &v }

// TestMCPToolCall_DurationMs_RoundTrip pins the new DurationMs *int64 field
// (US1 FR-002) via a Marshal → Unmarshal round trip. The legacy SDK struct
// had no DurationMs field at all — codex 0.121.0 sends one on every
// completed mcpToolCall.
func TestMCPToolCall_DurationMs_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		in             MCPToolCall
		wantDurationMs int64
	}{
		{
			name: "DurationMs round trip",
			in: MCPToolCall{
				ID:         "call_rt_1",
				ServerName: "docs",
				ToolName:   "search",
				DurationMs: ptrInt64(12345),
			},
			wantDurationMs: 12345,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(&tt.in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got MCPToolCall
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.DurationMs == nil {
				t.Fatalf("DurationMs is nil after round trip; want pointer to %d", tt.wantDurationMs)
			}
			if *got.DurationMs != tt.wantDurationMs {
				t.Errorf("*DurationMs = %d, want %d", *got.DurationMs, tt.wantDurationMs)
			}
		})
	}
}
