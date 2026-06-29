package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// cloneRaw returns an independent copy of raw so callers can retain it
// beyond the lifetime of the current parse buffer.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	return cp
}

func extractUnknownEventIDs(raw json.RawMessage) (threadID, turnID, itemID string) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return "", "", ""
	}
	return env.resolveIDs()
}

// identifiersEnvelope extracts thread/turn/item IDs from the common event
// shape. Supports both flat ("threadId") and nested ("thread.id") forms
// since the codex app-server emits both historically.
type identifiersEnvelope struct {
	ThreadID    string            `json:"threadId"`
	TurnID      string            `json:"turnId"`
	ItemID      string            `json:"itemId"`
	ThreadObj   *idWrapper        `json:"thread,omitempty"`
	TurnObj     *idWrapper        `json:"turn,omitempty"`
	ItemObj     json.RawMessage   `json:"item,omitempty"`
	DeltaObj    json.RawMessage   `json:"delta,omitempty"`
	Status      string            `json:"status,omitempty"`
	UsageObj    *types.TokenUsage `json:"usage,omitempty"`
	Code        string            `json:"code,omitempty"`
	Message     string            `json:"message,omitempty"`
	TokensFreed int64             `json:"tokens_freed,omitempty"`
	Strategy    string            `json:"strategy,omitempty"`
	Context     json.RawMessage   `json:"context,omitempty"`
}

type idWrapper struct {
	ID string `json:"id"`
}

// resolveIDs returns (threadID, turnID, itemID) preferring the flat fields
// and falling back to nested .Obj.ID when the flat field is empty.
func (e *identifiersEnvelope) resolveIDs() (threadID, turnID, itemID string) {
	threadID = e.ThreadID
	if threadID == "" && e.ThreadObj != nil {
		threadID = e.ThreadObj.ID
	}
	turnID = e.TurnID
	if turnID == "" && e.TurnObj != nil {
		turnID = e.TurnObj.ID
	}
	itemID = e.ItemID
	return
}

// unmarshalTo is a local helper mirroring unmarshalEnvelope but for
// arbitrary envelope types. Skips empty payloads and wraps decode errors
// in types.JSONDecodeError.
func unmarshalTo(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return types.NewJSONDecodeError(string(raw), err)
	}
	return nil
}

func parseErrorEvent(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	ev := &types.ErrorEvent{Code: env.Code, Message: env.Message}
	if len(env.Context) > 0 {
		cp := make(json.RawMessage, len(env.Context))
		copy(cp, env.Context)
		ev.Context = cp
	}
	return ev, nil
}

func unmarshalEnvelope(raw json.RawMessage, env *identifiersEnvelope) error {
	if len(raw) == 0 {
		// An empty params block is permissible for some notifications.
		return nil
	}
	if err := json.Unmarshal(raw, env); err != nil {
		return types.NewJSONDecodeError(string(raw), err)
	}
	return nil
}

// extractItemID reads .id from an item payload, returning "" if absent.
func extractItemID(raw json.RawMessage) string {
	var w struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &w)
	return w.ID
}
