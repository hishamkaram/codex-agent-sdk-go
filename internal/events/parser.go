package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ParseEvent translates a JSON-RPC notification into a typed
// types.ThreadEvent. Unrecognized methods return a *types.UnknownEvent.
//
// Wire methods covered by this parser must stay in sync with the v2
// schema emitted by `codex app-server generate-json-schema`. The
// fixture-replay test in this package fails if a method observed on the
// wire falls through to UnknownEvent — that's the enforcement mechanism.
func ParseEvent(n jsonrpc.Notification) (types.ThreadEvent, error) {
	switch n.Method {
	// --- Thread lifecycle ---
	case "thread/started":
		return parseThreadStarted(n.Params)
	case "thread/archived":
		return parseSimpleThreadEvent(n.Params, func(id string) types.ThreadEvent {
			return &types.ThreadArchived{ThreadID: id}
		})
	case "thread/unarchived":
		return parseSimpleThreadEvent(n.Params, func(id string) types.ThreadEvent {
			return &types.ThreadUnarchived{ThreadID: id}
		})
	case "thread/closed":
		return parseSimpleThreadEvent(n.Params, func(id string) types.ThreadEvent {
			return &types.ThreadClosed{ThreadID: id}
		})
	case "thread/name/updated":
		return parseThreadNameUpdated(n.Params)
	case "thread/status/changed":
		return parseThreadStatusChanged(n.Params)
	case "thread/compacted":
		return parseContextCompacted(n.Params)
	case "compaction_event": // v0.1.0 forward-compat; real wire is thread/compacted
		return parseContextCompacted(n.Params)
	case "thread/tokenUsage/updated":
		return parseTokenUsageUpdated(n.Params)

	// --- Turn ---
	case "turn/started":
		return parseTurnStarted(n.Params)
	case "turn/completed":
		return parseTurnCompleted(n.Params)
	case "turn/failed": // not in v2 schema; kept for forward-compat
		return parseTurnFailed(n.Params)
	case "turn/diff/updated":
		return parseTurnDiffUpdated(n.Params)
	case "turn/plan/updated":
		return parseTurnPlanUpdated(n.Params)

	// --- Items ---
	case "item/started":
		return parseItemStarted(n.Params)
	case "item/updated": // not in v2 schema; kept for forward-compat
		return parseItemUpdated(n.Params)
	case "item/completed":
		return parseItemCompleted(n.Params)

	// --- Items: streaming deltas (normalized into *ItemUpdated) ---
	case "item/agentMessage/delta":
		return parseFlatDelta(n.Params, "delta", func(s string) types.ItemDelta {
			return &types.AgentMessageDelta{TextChunk: s}
		})
	case "item/commandExecution/outputDelta":
		return parseFlatDelta(n.Params, "delta", func(s string) types.ItemDelta {
			return &types.CommandOutputDelta{OutputChunk: s}
		})
	case "item/fileChange/outputDelta":
		return parseFlatDelta(n.Params, "delta", func(s string) types.ItemDelta {
			return &types.FileChangeOutputDelta{DiffChunk: s}
		})
	case "item/plan/delta":
		return parseFlatDelta(n.Params, "delta", func(s string) types.ItemDelta {
			return &types.PlanDelta{Chunk: s}
		})
	case "item/reasoning/textDelta":
		return parseReasoningTextDelta(n.Params)
	case "item/reasoning/summaryTextDelta":
		return parseReasoningSummaryTextDelta(n.Params)
	case "item/reasoning/summaryPartAdded":
		return parseReasoningSummaryPartAdded(n.Params)
	case "item/mcpToolCall/progress":
		return parseMCPToolCallProgress(n.Params)
	case "item/commandExecution/terminalInteraction":
		return parseTerminalInteraction(n.Params)

	// --- Items: guardian auto-approval review ---
	case "item/autoApprovalReview/started":
		return parseGuardianReviewStarted(n.Params)
	case "item/autoApprovalReview/completed":
		return parseGuardianReviewCompleted(n.Params)

	// --- Realtime (voice) ---
	case "thread/realtime/started":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeStarted{ThreadID: id, Params: p}
		})
	case "thread/realtime/closed":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeClosed{ThreadID: id, Params: p}
		})
	case "thread/realtime/error":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeError{ThreadID: id, Params: p}
		})
	case "thread/realtime/itemAdded":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeItemAdded{ThreadID: id, Params: p}
		})
	case "thread/realtime/outputAudio/delta":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeOutputAudioDelta{ThreadID: id, Params: p}
		})
	case "thread/realtime/sdp":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeSdp{ThreadID: id, Params: p}
		})
	case "thread/realtime/transcript/delta":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeTranscriptDelta{ThreadID: id, Params: p}
		})
	case "thread/realtime/transcript/done":
		return wrapRealtime(n.Params, func(id string, p json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeTranscriptDone{ThreadID: id, Params: p}
		})

	// --- MCP ---
	case "mcpServer/startupStatus/updated":
		return parseMCPServerStartupStatus(n.Params)
	case "mcpServer/oauthLogin/completed":
		return parseMCPServerOAuthLoginCompleted(n.Params)

	// --- Account + model ---
	case "account/login/completed":
		return parseAccountLoginCompleted(n.Params)
	case "account/rateLimits/updated":
		return parseAccountRateLimitsUpdated(n.Params)
	case "account/updated":
		return parseAccountUpdated(n.Params)
	case "model/rerouted":
		return parseModelRerouted(n.Params)

	// --- System / filesystem / apps ---
	case "configWarning":
		return parseConfigWarning(n.Params)
	case "deprecationNotice":
		return parseDeprecationNotice(n.Params)
	case "fs/changed":
		return parseFsChanged(n.Params)
	case "skills/changed":
		return &types.SkillsChanged{}, nil
	case "app/list/updated":
		return parseAppListUpdated(n.Params)
	case "serverRequest/resolved":
		return parseServerRequestResolved(n.Params)

	// --- Windows platform ---
	case "windows/worldWritableWarning":
		return parseWindowsWorldWritableWarning(n.Params)
	case "windowsSandbox/setupCompleted":
		return parseWindowsSandboxSetupCompleted(n.Params)

	// --- Fuzzy file search ---
	case "fuzzyFileSearch/sessionUpdated":
		return &types.FuzzyFileSearchSessionUpdated{Params: cloneRaw(n.Params)}, nil
	case "fuzzyFileSearch/sessionCompleted":
		return &types.FuzzyFileSearchSessionCompleted{Params: cloneRaw(n.Params)}, nil

	// --- Errors ---
	case "error":
		return parseErrorEvent(n.Params)

	default:
		return &types.UnknownEvent{Method: n.Method, Params: cloneRaw(n.Params)}, nil
	}
}

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
	// Real wire shape (CLI 0.121.0): params has
	//   {"threadId","turnId","tokenUsage":{"last":{…},"total":{…},
	//    "modelContextWindow":258400}}
	// "last" is the per-turn slice; "total" is the running thread total.
	// The SDK surfaces "total" as the canonical Usage on TokenUsageUpdated
	// so callers tracking lifetime cost see the cumulative figure.
	// Also accept the flat shape {"usage":{…}} for forward-compat.
	var env struct {
		ThreadID   string `json:"threadId"`
		TokenUsage *struct {
			Total *types.TokenUsage `json:"total,omitempty"`
			Last  *types.TokenUsage `json:"last,omitempty"`
		} `json:"tokenUsage,omitempty"`
		Usage *types.TokenUsage `json:"usage,omitempty"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	var usage types.TokenUsage
	switch {
	case env.TokenUsage != nil && env.TokenUsage.Total != nil:
		usage = *env.TokenUsage.Total
	case env.TokenUsage != nil && env.TokenUsage.Last != nil:
		usage = *env.TokenUsage.Last
	case env.Usage != nil:
		usage = *env.Usage
	}
	return &types.TokenUsageUpdated{ThreadID: env.ThreadID, Usage: usage}, nil
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

// --- v0.2.0 expansion: helper parsers ---

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

func wrapRealtime(raw json.RawMessage, build func(threadID string, params json.RawMessage) types.ThreadEvent) (types.ThreadEvent, error) {
	var ids struct {
		ThreadID string `json:"threadId"`
	}
	_ = unmarshalTo(raw, &ids)
	return build(ids.ThreadID, cloneRaw(raw)), nil
}

func parseMCPServerStartupStatus(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Name   string          `json:"name"`
		Status json.RawMessage `json:"status"`
		Error  *string         `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.MCPServerStartupStatusUpdated{
		Name:   env.Name,
		Status: cloneRaw(env.Status),
		Error:  env.Error,
	}, nil
}

func parseMCPServerOAuthLoginCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Name    string  `json:"name"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.MCPServerOAuthLoginCompleted{Name: env.Name, Success: env.Success, Error: env.Error}, nil
}

func parseAccountLoginCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Success bool    `json:"success"`
		LoginID *string `json:"loginId"`
		Error   *string `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountLoginCompleted{Success: env.Success, LoginID: env.LoginID, Error: env.Error}, nil
}

func parseAccountRateLimitsUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		RateLimits json.RawMessage `json:"rateLimits"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountRateLimitsUpdated{RateLimits: cloneRaw(env.RateLimits)}, nil
}

func parseAccountUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		AuthMode json.RawMessage `json:"authMode"`
		PlanType json.RawMessage `json:"planType"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AccountUpdated{AuthMode: cloneRaw(env.AuthMode), PlanType: cloneRaw(env.PlanType)}, nil
}

func parseModelRerouted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		FromModel string          `json:"fromModel"`
		ToModel   string          `json:"toModel"`
		Reason    json.RawMessage `json:"reason"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ModelRerouted{
		ThreadID:  env.ThreadID,
		TurnID:    env.TurnID,
		FromModel: env.FromModel,
		ToModel:   env.ToModel,
		Reason:    cloneRaw(env.Reason),
	}, nil
}

func parseConfigWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Summary string          `json:"summary"`
		Details *string         `json:"details"`
		Path    *string         `json:"path"`
		Range   json.RawMessage `json:"range"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ConfigWarning{Summary: env.Summary, Details: env.Details, Path: env.Path, Range: cloneRaw(env.Range)}, nil
}

func parseDeprecationNotice(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Summary string  `json:"summary"`
		Details *string `json:"details"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.DeprecationNotice{Summary: env.Summary, Details: env.Details}, nil
}

func parseFsChanged(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		WatchID      string   `json:"watchId"`
		ChangedPaths []string `json:"changedPaths"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.FsChanged{WatchID: env.WatchID, ChangedPaths: env.ChangedPaths}, nil
}

func parseAppListUpdated(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.AppListUpdated{Data: cloneRaw(env.Data)}, nil
}

func parseServerRequestResolved(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.ServerRequestResolved{ThreadID: env.ThreadID, RequestID: cloneRaw(env.RequestID)}, nil
}

func parseWindowsWorldWritableWarning(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		ExtraCount  int      `json:"extraCount"`
		FailedScan  bool     `json:"failedScan"`
		SamplePaths []string `json:"samplePaths"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.WindowsWorldWritableWarning{
		ExtraCount:  env.ExtraCount,
		FailedScan:  env.FailedScan,
		SamplePaths: env.SamplePaths,
	}, nil
}

func parseWindowsSandboxSetupCompleted(raw json.RawMessage) (types.ThreadEvent, error) {
	var env struct {
		Mode    json.RawMessage `json:"mode"`
		Success bool            `json:"success"`
		Error   *string         `json:"error"`
	}
	if err := unmarshalTo(raw, &env); err != nil {
		return nil, err
	}
	return &types.WindowsSandboxSetupCompleted{
		Success: env.Success,
		Mode:    cloneRaw(env.Mode),
		Error:   env.Error,
	}, nil
}
