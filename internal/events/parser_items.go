package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// parseFlatDelta handles the item/<variant>/delta methods where params
// carry {threadId, turnId, itemId, <fieldName>: "string"}. The delta
// string is fed to build which returns the typed ItemDelta subtype.
func parseFlatDelta(raw json.RawMessage, fieldName string, build func(s string) types.ItemDelta) (types.ThreadEvent, error) {
	// Two-phase: unmarshal the common ids into a struct, then unmarshal
	// the field name into a flat string via a second pass.
	var ids struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
	}
	if err := unmarshalTo(raw, &ids); err != nil {
		return nil, err
	}
	// Extract fieldName as a string.
	var payload map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, types.NewJSONDecodeError(string(raw), err)
		}
	}
	var text string
	if v, ok := payload[fieldName]; ok {
		if err := json.Unmarshal(v, &text); err != nil {
			return nil, types.NewJSONDecodeError(string(raw), err)
		}
	}
	return &types.ItemUpdated{
		ThreadID: ids.ThreadID,
		TurnID:   ids.TurnID,
		ItemID:   ids.ItemID,
		Delta:    build(text),
	}, nil
}

func parseReasoningTextDelta(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		ItemID       string `json:"itemId"`
		Delta        string `json:"delta"`
		ContentIndex int    `json:"contentIndex"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.ReasoningTextDelta{TextChunk: env.Delta, ContentIndex: env.ContentIndex},
	}, nil
}

func parseReasoningSummaryTextDelta(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		ItemID       string `json:"itemId"`
		Delta        string `json:"delta"`
		SummaryIndex int    `json:"summaryIndex"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.ReasoningSummaryTextDelta{SummaryChunk: env.Delta, SummaryIndex: env.SummaryIndex},
	}, nil
}

func parseReasoningSummaryPartAdded(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID     string `json:"threadId"`
		TurnID       string `json:"turnId"`
		ItemID       string `json:"itemId"`
		SummaryIndex int    `json:"summaryIndex"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.ReasoningSummaryPartAdded{SummaryIndex: env.SummaryIndex},
	}, nil
}

func parseMCPToolCallProgress(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Message  string `json:"message"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.MCPToolCallProgress{Message: env.Message},
	}, nil
}

func parseTerminalInteraction(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
		ItemID    string `json:"itemId"`
		ProcessID string `json:"processId"`
		Stdin     string `json:"stdin"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Delta:    &types.TerminalInteraction{ProcessID: env.ProcessID, Stdin: env.Stdin},
	}, nil
}

func parseGuardianReviewStarted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID     string          `json:"threadId"`
		TurnID       string          `json:"turnId"`
		ReviewID     string          `json:"reviewId"`
		TargetItemID *string         `json:"targetItemId"`
		Action       json.RawMessage `json:"action"`
		Review       json.RawMessage `json:"review"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemGuardianApprovalReviewStarted{
		ThreadID:     env.ThreadID,
		TurnID:       env.TurnID,
		ReviewID:     env.ReviewID,
		TargetItemID: env.TargetItemID,
		Action:       cloneRaw(env.Action),
		Review:       cloneRaw(env.Review),
	}, nil
}

func parseGuardianReviewCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID       string          `json:"threadId"`
		TurnID         string          `json:"turnId"`
		ReviewID       string          `json:"reviewId"`
		TargetItemID   *string         `json:"targetItemId"`
		Action         json.RawMessage `json:"action"`
		Review         json.RawMessage `json:"review"`
		DecisionSource json.RawMessage `json:"decisionSource"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ItemGuardianApprovalReviewCompleted{
		ThreadID:       env.ThreadID,
		TurnID:         env.TurnID,
		ReviewID:       env.ReviewID,
		TargetItemID:   env.TargetItemID,
		Action:         cloneRaw(env.Action),
		Review:         cloneRaw(env.Review),
		DecisionSource: cloneRaw(env.DecisionSource),
	}, nil
}

func parseFileChangePatchUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		ItemID   string          `json:"itemId"`
		Changes  json.RawMessage `json:"changes"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.FileChangePatchUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		ItemID:   env.ItemID,
		Changes:  cloneRaw(env.Changes),
	}, nil
}
