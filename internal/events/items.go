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
	// Codex uses camelCase discriminators on the wire. See
	// types/items.go for the complete mapping. Fall through to
	// UnknownItem for any value not in this switch.
	switch disc.Type {
	case "agentMessage":
		var it types.AgentMessage
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("agentMessage", raw, err)
		}
		return &it, nil
	case "userMessage":
		var it types.UserMessage
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("userMessage", raw, err)
		}
		return &it, nil
	case "commandExecution":
		var it types.CommandExecution
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("commandExecution", raw, err)
		}
		return &it, nil
	case "fileChange":
		var it types.FileChange
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("fileChange", raw, err)
		}
		return &it, nil
	case "mcpToolCall":
		var it types.MCPToolCall
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("mcpToolCall", raw, err)
		}
		return &it, nil
	case "webSearch":
		var it types.WebSearch
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("webSearch", raw, err)
		}
		return &it, nil
	case "memoryRead":
		var it types.MemoryRead
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("memoryRead", raw, err)
		}
		return &it, nil
	case "memoryWrite":
		var it types.MemoryWrite
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("memoryWrite", raw, err)
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
	case "systemError":
		var it types.SystemError
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, wrapParseErr("systemError", raw, err)
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
		// Legacy discriminator (not in v2 schema). Map to text delta.
		var d types.ReasoningTextDelta
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
