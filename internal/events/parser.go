package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ParseEvent translates a JSON-RPC notification into a typed
// types.ThreadEvent. Unrecognized methods return a *types.UnknownEvent.
func ParseEvent(n jsonrpc.Notification) (types.ThreadEvent, error) {
	switch n.Method {
	case "thread/started":
		return parseThreadStarted(n.Params)
	case "turn/started":
		return parseTurnStarted(n.Params)
	case "turn/completed":
		return parseTurnCompleted(n.Params)
	case "turn/failed":
		return parseTurnFailed(n.Params)
	case "item/started":
		return parseItemStarted(n.Params)
	case "item/updated":
		return parseItemUpdated(n.Params)
	case "item/agentMessage/delta":
		return parseAgentMessageDeltaEvent(n.Params)
	case "item/completed":
		return parseItemCompleted(n.Params)
	case "thread/tokenUsage/updated":
		return parseTokenUsageUpdated(n.Params)
	case "compaction_event":
		return parseCompactionEvent(n.Params)
	case "error":
		return parseErrorEvent(n.Params)
	default:
		cp := make(json.RawMessage, len(n.Params))
		copy(cp, n.Params)
		return &types.UnknownEvent{Method: n.Method, Params: cp}, nil
	}
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

func parseThreadStarted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, _, _ := env.resolveIDs()
	return &types.ThreadStarted{ThreadID: threadID}, nil
}

func parseTurnStarted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, _ := env.resolveIDs()
	return &types.TurnStarted{ThreadID: threadID, TurnID: turnID}, nil
}

func parseTurnCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, _ := env.resolveIDs()
	ev := &types.TurnCompleted{
		ThreadID: threadID,
		TurnID:   turnID,
		Status:   env.Status,
	}
	if env.UsageObj != nil {
		ev.Usage = *env.UsageObj
	}
	return ev, nil
}

func parseTurnFailed(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, _ := env.resolveIDs()
	return &types.TurnFailed{
		ThreadID: threadID,
		TurnID:   turnID,
		Code:     env.Code,
		Message:  env.Message,
	}, nil
}

func parseItemStarted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, itemID := env.resolveIDs()
	if len(env.ItemObj) == 0 {
		return nil, types.NewMessageParseError("item/started missing item field", string(raw))
	}
	item, err := ParseItem(env.ItemObj)
	if err != nil {
		return nil, err
	}
	// If the item itself carries an id and the outer didn't, fall back to it.
	if itemID == "" {
		itemID = extractItemID(env.ItemObj)
	}
	return &types.ItemStarted{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Item:     item,
	}, nil
}

func parseItemUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, itemID := env.resolveIDs()
	if len(env.DeltaObj) == 0 {
		return nil, types.NewMessageParseError("item/updated missing delta field", string(raw))
	}
	delta, err := ParseItemDelta(env.DeltaObj)
	if err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Delta:    delta,
	}, nil
}

// parseAgentMessageDeltaEvent handles the real streaming text channel for
// agent_message items. Wire shape (verified against spike transcript):
//
//	{"method":"item/agentMessage/delta",
//	 "params":{"threadId":"...","turnId":"...","itemId":"msg_...",
//	           "delta":"OK"}}
//
// The flat "delta" string is mapped to a types.AgentMessageDelta inside
// an ItemUpdated so callers see a single ItemUpdated event type across
// all streaming item variants.
func parseAgentMessageDeltaEvent(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, types.NewJSONDecodeError(string(raw), err)
		}
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.AgentMessageDelta{TextChunk: env.Delta},
	}, nil
}

func parseItemCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, turnID, itemID := env.resolveIDs()
	if len(env.ItemObj) == 0 {
		return nil, types.NewMessageParseError("item/completed missing item field", string(raw))
	}
	item, err := ParseItem(env.ItemObj)
	if err != nil {
		return nil, err
	}
	if itemID == "" {
		itemID = extractItemID(env.ItemObj)
	}
	return &types.ItemCompleted{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Item:     item,
	}, nil
}

func parseTokenUsageUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, _, _ := env.resolveIDs()
	var usage types.TokenUsage
	if env.UsageObj != nil {
		usage = *env.UsageObj
	}
	return &types.TokenUsageUpdated{ThreadID: threadID, Usage: usage}, nil
}

func parseCompactionEvent(raw json.RawMessage) (types.ThreadEvent, error) {
	var env identifiersEnvelope
	if err := unmarshalEnvelope(raw, &env); err != nil {
		return nil, err
	}
	threadID, _, _ := env.resolveIDs()
	return &types.CompactionEvent{
		ThreadID:    threadID,
		TokensFreed: env.TokensFreed,
		Strategy:    env.Strategy,
	}, nil
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
