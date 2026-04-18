package types

import "testing"

func TestItemType_EveryKnownItem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		item ThreadItem
		want string
	}{
		{&AgentMessage{}, "agent_message"},
		{&UserMessage{}, "user_message"},
		{&CommandExecution{}, "command_execution"},
		{&FileChange{}, "file_change"},
		{&MCPToolCall{}, "mcp_tool_call"},
		{&WebSearch{}, "web_search"},
		{&MemoryRead{}, "memory_read"},
		{&MemoryWrite{}, "memory_write"},
		{&Plan{}, "plan"},
		{&Reasoning{}, "reasoning"},
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
		{&AgentMessageDelta{}, "agent_message_delta"},
		{&ReasoningDelta{}, "reasoning_delta"},
		{&CommandOutputDelta{}, "command_output_delta"},
		{&FileChangeOutputDelta{}, "file_change_output_delta"},
		{&MCPToolCallProgress{}, "mcp_tool_call_progress"},
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
