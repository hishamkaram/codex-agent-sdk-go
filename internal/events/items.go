// Package events translates raw JSON-RPC notification payloads (from the
// codex app-server) into the typed events and items exposed by package
// types.
//
// The parser is permissive: on any unrecognized discriminator it returns a
// types.UnknownItem / types.UnknownDelta / types.UnknownEvent wrapping the
// raw payload. Callers must type-switch on these to handle new wire shapes
// introduced by future codex CLI versions.
package events

import (
	"encoding/json"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ParseItem decodes a raw item payload. The outer envelope must have a
// "type" field; other fields are shape-specific.
func ParseItem(raw json.RawMessage) (types.ThreadItem, error) {
	if len(raw) == 0 {
		return nil, types.NewMessageParseError("empty item payload", "")
	}
	var disc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &disc); err != nil {
		return nil, types.NewJSONDecodeError(string(raw), err)
	}
	switch disc.Type {
	case "agent_message":
		var it types.AgentMessage
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("agent_message", raw, err)
		}
		return &it, nil
	case "user_message":
		var it types.UserMessage
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("user_message", raw, err)
		}
		return &it, nil
	case "command_execution":
		var it types.CommandExecution
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("command_execution", raw, err)
		}
		return &it, nil
	case "file_change":
		var it types.FileChange
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("file_change", raw, err)
		}
		return &it, nil
	case "mcp_tool_call":
		var it types.MCPToolCall
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("mcp_tool_call", raw, err)
		}
		return &it, nil
	case "web_search":
		var it types.WebSearch
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("web_search", raw, err)
		}
		return &it, nil
	case "memory_read":
		var it types.MemoryRead
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("memory_read", raw, err)
		}
		return &it, nil
	case "memory_write":
		var it types.MemoryWrite
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("memory_write", raw, err)
		}
		return &it, nil
	case "plan":
		var it types.Plan
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("plan", raw, err)
		}
		return &it, nil
	case "reasoning":
		var it types.Reasoning
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("reasoning", raw, err)
		}
		return &it, nil
	default:
		// Forward-compat: return an UnknownItem with the raw payload.
		cp := make(json.RawMessage, len(raw))
		copy(cp, raw)
		return &types.UnknownItem{Type: disc.Type, Raw: cp}, nil
	}
}

// ParseItemDelta decodes a raw item-delta payload. Follows the same
// discriminator convention as ParseItem.
func ParseItemDelta(raw json.RawMessage) (types.ItemDelta, error) {
	if len(raw) == 0 {
		return nil, types.NewMessageParseError("empty delta payload", "")
	}
	var disc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &disc); err != nil {
		return nil, types.NewJSONDecodeError(string(raw), err)
	}
	switch disc.Type {
	case "agent_message_delta":
		var d types.AgentMessageDelta
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, wrapParseErr("agent_message_delta", raw, err)
		}
		return &d, nil
	case "reasoning_delta":
		var d types.ReasoningDelta
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, wrapParseErr("reasoning_delta", raw, err)
		}
		return &d, nil
	case "command_output_delta":
		var d types.CommandOutputDelta
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, wrapParseErr("command_output_delta", raw, err)
		}
		return &d, nil
	case "file_change_output_delta":
		var d types.FileChangeOutputDelta
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, wrapParseErr("file_change_output_delta", raw, err)
		}
		return &d, nil
	case "mcp_tool_call_progress":
		var d types.MCPToolCallProgress
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, wrapParseErr("mcp_tool_call_progress", raw, err)
		}
		return &d, nil
	default:
		cp := make(json.RawMessage, len(raw))
		copy(cp, raw)
		return &types.UnknownDelta{Type: disc.Type, Raw: cp}, nil
	}
}

func wrapParseErr(kind string, raw json.RawMessage, err error) error {
	return types.NewMessageParseError(
		fmt.Sprintf("unmarshal %s: %v", kind, err),
		string(raw),
	)
}
