package events

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// eventParsers maps each JSON-RPC notification method to the parser that
// produces its typed types.ThreadEvent. It is the lookup-table form of the
// former ParseEvent switch — every entry preserves the exact parser binding,
// including the forward-compat aliases (compaction_event → parseContextCompacted,
// turn/failed, item/updated) and the inline constructors for the simple-thread,
// flat-delta, realtime, hook, and raw-passthrough families.
//
// Wire methods covered here must stay in sync with the v2 schema emitted by
// `codex app-server generate-json-schema`. The fixture-replay test in this
// package fails if a method observed on the wire falls through to UnknownEvent.
var eventParsers = map[string]func(json.RawMessage) (types.ThreadEvent, error){
	// --- Thread lifecycle ---
	"thread/started": parseThreadStarted,
	"thread/archived": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseSimpleThreadEvent(params, func(id string) types.ThreadEvent {
			return &types.ThreadArchived{ThreadID: id}
		})
	},
	"thread/unarchived": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseSimpleThreadEvent(params, func(id string) types.ThreadEvent {
			return &types.ThreadUnarchived{ThreadID: id}
		})
	},
	"thread/closed": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseSimpleThreadEvent(params, func(id string) types.ThreadEvent {
			return &types.ThreadClosed{ThreadID: id}
		})
	},
	"thread/deleted": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseSimpleThreadEvent(params, func(id string) types.ThreadEvent {
			return &types.ThreadDeleted{ThreadID: id}
		})
	},
	"thread/name/updated":       parseThreadNameUpdated,
	"thread/status/changed":     parseThreadStatusChanged,
	"thread/settings/updated":   parseThreadSettingsUpdated,
	"thread/compacted":          parseContextCompacted,
	"compaction_event":          parseContextCompacted, // v0.1.0 forward-compat; real wire is thread/compacted
	"thread/tokenUsage/updated": parseTokenUsageUpdated,
	"thread/goal/updated":       parseThreadGoalUpdated,
	"thread/goal/cleared": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseSimpleThreadEvent(params, func(id string) types.ThreadEvent {
			return &types.ThreadGoalCleared{ThreadID: id}
		})
	},

	// --- Turn ---
	"turn/started":            parseTurnStarted,
	"turn/completed":          parseTurnCompleted,
	"turn/failed":             parseTurnFailed, // not in v2 schema; kept for forward-compat
	"turn/diff/updated":       parseTurnDiffUpdated,
	"turn/plan/updated":       parseTurnPlanUpdated,
	"turn/moderationMetadata": parseTurnModerationMetadata,

	// --- Items ---
	"item/started":   parseItemStarted,
	"item/updated":   parseItemUpdated, // not in v2 schema; kept for forward-compat
	"item/completed": parseItemCompleted,

	// --- Items: streaming deltas (normalized into *ItemUpdated) ---
	"item/agentMessage/delta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseFlatDelta(params, "delta", func(s string) types.ItemDelta {
			return &types.AgentMessageDelta{TextChunk: s}
		})
	},
	"item/commandExecution/outputDelta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseFlatDelta(params, "delta", func(s string) types.ItemDelta {
			return &types.CommandOutputDelta{OutputChunk: s}
		})
	},
	"item/fileChange/outputDelta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseFlatDelta(params, "delta", func(s string) types.ItemDelta {
			return &types.FileChangeOutputDelta{DiffChunk: s}
		})
	},
	"item/fileChange/patchUpdated": parseFileChangePatchUpdated,
	"item/plan/delta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseFlatDelta(params, "delta", func(s string) types.ItemDelta {
			return &types.PlanDelta{Chunk: s}
		})
	},
	"item/reasoning/textDelta":                  parseReasoningTextDelta,
	"item/reasoning/summaryTextDelta":           parseReasoningSummaryTextDelta,
	"item/reasoning/summaryPartAdded":           parseReasoningSummaryPartAdded,
	"item/mcpToolCall/progress":                 parseMCPToolCallProgress,
	"item/commandExecution/terminalInteraction": parseTerminalInteraction,

	// --- Items: guardian auto-approval review ---
	"item/autoApprovalReview/started":   parseGuardianReviewStarted,
	"item/autoApprovalReview/completed": parseGuardianReviewCompleted,

	// --- Realtime (voice) ---
	"thread/realtime/started": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeStarted{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/closed": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeClosed{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/error": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeError{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/itemAdded": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeItemAdded{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/outputAudio/delta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeOutputAudioDelta{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/sdp": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeSdp{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/transcript/delta": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeTranscriptDelta{ThreadID: id, Params: raw}
		})
	},
	"thread/realtime/transcript/done": func(params json.RawMessage) (types.ThreadEvent, error) {
		return wrapRealtime(params, func(id string, raw json.RawMessage) types.ThreadEvent {
			return &types.ThreadRealtimeTranscriptDone{ThreadID: id, Params: raw}
		})
	},

	// --- MCP ---
	"mcpServer/startupStatus/updated": parseMCPServerStartupStatus,
	"mcpServer/oauthLogin/completed":  parseMCPServerOAuthLoginCompleted,

	// --- Account + model ---
	"account/login/completed":       parseAccountLoginCompleted,
	"account/rateLimits/updated":    parseAccountRateLimitsUpdated,
	"account/updated":               parseAccountUpdated,
	"model/rerouted":                parseModelRerouted,
	"model/safetyBuffering/updated": parseModelSafetyBufferingUpdated,
	"model/verification":            parseModelVerification,

	// --- System / filesystem / apps ---
	"command/exec/outputDelta":            parseCommandExecOutputDelta,
	"process/outputDelta":                 parseProcessOutputDelta,
	"process/exited":                      parseProcessExited,
	"remoteControl/status/changed":        parseRemoteControlStatusChanged,
	"configWarning":                       parseConfigWarning,
	"warning":                             parseWarning,
	"guardianWarning":                     parseGuardianWarning,
	"deprecationNotice":                   parseDeprecationNotice,
	"fs/changed":                          parseFsChanged,
	"app/list/updated":                    parseAppListUpdated,
	"serverRequest/resolved":              parseServerRequestResolved,
	"externalAgentConfig/import/progress": parseExternalAgentConfigImportProgress,

	// --- Windows platform ---
	"windows/worldWritableWarning":  parseWindowsWorldWritableWarning,
	"windowsSandbox/setupCompleted": parseWindowsSandboxSetupCompleted,

	// --- Hooks (v0.2.0 observer; require --enable hooks) ---
	"hook/started": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseHookEvent(params, true)
	},
	"hook/completed": func(params json.RawMessage) (types.ThreadEvent, error) {
		return parseHookEvent(params, false)
	},

	// --- Errors ---
	"error": parseErrorEvent,
}

// rawEventBuilders holds the infallible event constructors — methods whose
// payload is passed through verbatim (or which carry no payload) and therefore
// never fail to parse. Keeping them in a no-error table (rather than as
// always-return-nil closures in eventParsers) keeps each builder honest about
// having no failure mode.
var rawEventBuilders = map[string]func(json.RawMessage) types.ThreadEvent{
	"externalAgentConfig/import/completed": func(params json.RawMessage) types.ThreadEvent {
		return &types.ExternalAgentConfigImportCompleted{Params: cloneRaw(params)}
	},
	"skills/changed": func(_ json.RawMessage) types.ThreadEvent {
		return &types.SkillsChanged{}
	},
	"fuzzyFileSearch/sessionUpdated": func(params json.RawMessage) types.ThreadEvent {
		return &types.FuzzyFileSearchSessionUpdated{Params: cloneRaw(params)}
	},
	"fuzzyFileSearch/sessionCompleted": func(params json.RawMessage) types.ThreadEvent {
		return &types.FuzzyFileSearchSessionCompleted{Params: cloneRaw(params)}
	},
}

// ParseEvent translates a JSON-RPC notification into a typed
// types.ThreadEvent. Unrecognized methods return a *types.UnknownEvent.
//
// Dispatch is table-driven: eventParsers for the fallible parsers,
// rawEventBuilders for the infallible passthrough constructors. The default
// branch below mirrors the former switch default — it extracts whatever
// thread/turn/item IDs are present so the UnknownEvent still routes correctly
// downstream.
func ParseEvent(n jsonrpc.Notification) (types.ThreadEvent, error) {
	if parse, ok := eventParsers[n.Method]; ok {
		return parse(n.Params)
	}
	if build, ok := rawEventBuilders[n.Method]; ok {
		return build(n.Params), nil
	}
	threadID, turnID, itemID := extractUnknownEventIDs(n.Params)
	return &types.UnknownEvent{
		Method:   n.Method,
		Params:   cloneRaw(n.Params),
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
	}, nil
}
