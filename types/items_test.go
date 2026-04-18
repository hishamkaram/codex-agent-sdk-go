package types

import "testing"

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
