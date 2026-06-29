package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
	// Real wire shape (CLI 0.121.0): params carries {"threadId","turn":
	// {"id","status","error":{"message":...},"startedAt","completedAt",
	// "durationMs","items":[]}}. Earlier design-time assumptions used
	// flat {"turnId","status","usage"} — we tolerate both for
	// forward-compat.
	var env struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		} `json:"turn,omitempty"`
		Status string            `json:"status,omitempty"`
		Usage  *types.TokenUsage `json:"usage,omitempty"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	turnID := env.TurnID
	status := env.Status
	if env.Turn != nil {
		if turnID == "" {
			turnID = env.Turn.ID
		}
		if status == "" {
			status = env.Turn.Status
		}
	}
	ev := &types.TurnCompleted{
		ThreadID: env.ThreadID,
		TurnID:   turnID,
		Status:   status,
	}
	if env.Usage != nil {
		ev.Usage = *env.Usage
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
	// Real wire shape (CLI 0.121.0): params has
	//   {"threadId","turnId","tokenUsage":{"last":{…},"total":{…},
	//    "modelContextWindow":258400}}
	// "last" is the per-turn slice; "total" is the running thread total.
	// The SDK keeps "total" as the canonical Usage on TokenUsageUpdated for
	// compatibility, while also exposing LastUsage so billing callers can use
	// the per-turn slice and avoid double-counting cumulative snapshots.
	// Also accept the flat shape {"usage":{…}} for forward-compat.
	var env struct {
		ThreadID   string `json:"threadId"`
		TokenUsage *struct {
			Total              *types.TokenUsage `json:"total,omitempty"`
			Last               *types.TokenUsage `json:"last,omitempty"`
			ModelContextWindow int64             `json:"modelContextWindow,omitempty"`
		} `json:"tokenUsage,omitempty"`
		Usage *types.TokenUsage `json:"usage,omitempty"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	var usage types.TokenUsage
	var lastUsage types.TokenUsage
	var totalUsage types.TokenUsage
	switch {
	case env.TokenUsage != nil && env.TokenUsage.Total != nil:
		totalUsage = *env.TokenUsage.Total
		usage = totalUsage
		if env.TokenUsage.Last != nil {
			lastUsage = *env.TokenUsage.Last
		}
	case env.TokenUsage != nil && env.TokenUsage.Last != nil:
		lastUsage = *env.TokenUsage.Last
		usage = lastUsage
	case env.Usage != nil:
		usage = *env.Usage
	}
	var modelContextWindow int64
	if env.TokenUsage != nil {
		modelContextWindow = env.TokenUsage.ModelContextWindow
	}
	return &types.TokenUsageUpdated{
		ThreadID:           env.ThreadID,
		Usage:              usage,
		LastUsage:          lastUsage,
		TotalUsage:         totalUsage,
		ModelContextWindow: modelContextWindow,
	}, nil
}
