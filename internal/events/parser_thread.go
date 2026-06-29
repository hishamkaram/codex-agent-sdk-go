package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// parseSimpleThreadEvent parses {threadId: "..."} payloads into the event
// constructed by build.
func parseSimpleThreadEvent(raw json.RawMessage, build func(threadID string) types.ThreadEvent) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return build(env.ThreadID), nil
}

func parseThreadNameUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID   string  `json:"threadId"`
		ThreadName *string `json:"threadName"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ThreadNameUpdated{ThreadID: env.ThreadID, ThreadName: env.ThreadName}, nil
}

func parseThreadStatusChanged(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string          `json:"threadId"`
		Status   json.RawMessage `json:"status"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ThreadStatusChanged{ThreadID: env.ThreadID, Status: cloneRaw(env.Status)}, nil
}

func parseThreadSettingsUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID       string          `json:"threadId"`
		ThreadSettings json.RawMessage `json:"threadSettings"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ThreadSettingsUpdated{
		ThreadID:       env.ThreadID,
		ThreadSettings: cloneRaw(env.ThreadSettings),
	}, nil
}

func parseContextCompacted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ContextCompacted{ThreadID: env.ThreadID, TurnID: env.TurnID}, nil
}

func parseTurnDiffUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Diff     string `json:"diff"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.TurnDiffUpdated{ThreadID: env.ThreadID, TurnID: env.TurnID, Diff: env.Diff}, nil
}

func parseTurnPlanUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID    string          `json:"threadId"`
		TurnID      string          `json:"turnId"`
		Plan        json.RawMessage `json:"plan"`
		Explanation *string         `json:"explanation"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.TurnPlanUpdated{
		ThreadID:    env.ThreadID,
		TurnID:      env.TurnID,
		Plan:        cloneRaw(env.Plan),
		Explanation: env.Explanation,
	}, nil
}

func parseTurnModerationMetadata(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.TurnModerationMetadata{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		Metadata: cloneRaw(env.Metadata),
	}, nil
}

func parseThreadGoalUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID string          `json:"threadId"`
		TurnID   *string         `json:"turnId"`
		Goal     json.RawMessage `json:"goal"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ThreadGoalUpdated{
		ThreadID: env.ThreadID,
		TurnID:   env.TurnID,
		Goal:     cloneRaw(env.Goal),
	}, nil
}
