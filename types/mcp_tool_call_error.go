package types

import "encoding/json"

// MCPToolCallErrorField absorbs the wire-shape variance of MCPToolCall.Error.
// Codex 0.121.0 sends {"message": "..."} (object). Older servers sent a bare
// string. Null is also accepted. On any unknown shape the field is left empty
// with no error (forward-compat posture — the SDK's types package owns wire
// shape; consumers should never see parse errors from shape drift).
type MCPToolCallErrorField struct{ Message string }

func (e *MCPToolCallErrorField) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.Message != "" {
		e.Message = obj.Message
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Message = s
		return nil
	}
	return nil
}
