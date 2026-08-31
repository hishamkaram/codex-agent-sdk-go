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

// itemPtr constrains PT to be a *T that also satisfies types.ThreadItem, so
// decodeItem can return a typed pointer through the interface. Go infers PT
// from T at each itemDecoders entry (the constraint's core type is *T).
type itemPtr[T any] interface {
	*T
	types.ThreadItem
}

// decodeItem unmarshals raw into a fresh T and returns it as a ThreadItem,
// wrapping any decode failure with the wire discriminator for context. This
// is the single body that every concrete item case shares.
func decodeItem[T any, PT itemPtr[T]](kind string, raw json.RawMessage) (types.ThreadItem, error) {
	var it T
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil, wrapParseErr(kind, raw, err)
	}
	return PT(&it), nil
}

// itemDecoders maps each camelCase wire discriminator to its concrete-type
// decoder. Codex uses camelCase discriminators on the wire; see types/items.go
// for the complete mapping. Any discriminator absent from this table falls
// through to UnknownItem in ParseItem.
var itemDecoders = map[string]func(string, json.RawMessage) (types.ThreadItem, error){
	"agentMessage":        decodeItem[types.AgentMessage],
	"userMessage":         decodeItem[types.UserMessage],
	"commandExecution":    decodeItem[types.CommandExecution],
	"fileChange":          decodeItem[types.FileChange],
	"mcpToolCall":         decodeItem[types.MCPToolCall],
	"webSearch":           decodeItem[types.WebSearch],
	"memoryRead":          decodeItem[types.MemoryRead],
	"memoryWrite":         decodeItem[types.MemoryWrite],
	"plan":                decodeItem[types.Plan],
	"reasoning":           decodeItem[types.Reasoning],
	"systemError":         decodeItem[types.SystemError],
	"hookPrompt":          decodeItem[types.HookPrompt],
	"dynamicToolCall":     decodeItem[types.DynamicToolCall],
	"collabAgentToolCall": decodeItem[types.CollabAgentToolCall],
	"subAgentActivity":    decodeItem[types.SubAgentActivity],
	"imageView":           decodeItem[types.ImageView],
	"imageGeneration":     decodeItem[types.ImageGeneration],
	"enteredReviewMode":   decodeItem[types.EnteredReviewMode],
	"exitedReviewMode":    decodeItem[types.ExitedReviewMode],
	"contextCompaction":   decodeItem[types.ContextCompaction],
}

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
	if decode, ok := itemDecoders[disc.Type]; ok {
		return decode(disc.Type, raw)
	}
	// Forward-compat: return an UnknownItem with the raw payload.
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	return &types.UnknownItem{Type: disc.Type, Raw: cp}, nil
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
