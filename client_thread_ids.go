package codex

import "github.com/hishamkaram/codex-agent-sdk-go/types"

// threadIDExtractors is the ordered set of category extractors consulted by
// extractThreadIDFromEvent. Each event type is handled by exactly one extractor;
// the first to report ok wins. Splitting the former single 38-case type switch
// into category functions keeps each under the cyclomatic gate while preserving
// the exact recognized-type set (proven by TestExtractThreadIDFromEvent_Oracle).
var threadIDExtractors = []func(types.ThreadEvent) (string, bool){
	threadIDFromThreadLifecycleEvent,
	threadIDFromTurnEvent,
	threadIDFromItemEvent,
	threadIDFromRealtimeEvent,
	threadIDFromMCPEvent,
	threadIDFromMiscEvent,
}

// extractThreadIDFromEvent returns the ThreadID field of every event type
// that carries one. Returns "" for events that don't, including global
// warnings and UnknownEvent values whose payload did not expose a thread ID.
func extractThreadIDFromEvent(ev types.ThreadEvent) string {
	for _, extract := range threadIDExtractors {
		if id, ok := extract(ev); ok {
			return id
		}
	}
	return ""
}

func threadIDFromThreadLifecycleEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.ThreadStarted:
		return e.ThreadID, true
	case *types.ThreadArchived:
		return e.ThreadID, true
	case *types.ThreadUnarchived:
		return e.ThreadID, true
	case *types.ThreadClosed:
		return e.ThreadID, true
	case *types.ThreadDeleted:
		return e.ThreadID, true
	case *types.ThreadNameUpdated:
		return e.ThreadID, true
	case *types.ThreadStatusChanged:
		return e.ThreadID, true
	case *types.ThreadSettingsUpdated:
		return e.ThreadID, true
	case *types.ThreadGoalUpdated:
		return e.ThreadID, true
	case *types.ThreadGoalCleared:
		return e.ThreadID, true
	case *types.ContextCompacted:
		return e.ThreadID, true
	}
	return "", false
}

func threadIDFromTurnEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.TurnStarted:
		return e.ThreadID, true
	case *types.TurnCompleted:
		return e.ThreadID, true
	case *types.TurnFailed:
		return e.ThreadID, true
	case *types.TurnDiffUpdated:
		return e.ThreadID, true
	case *types.TurnPlanUpdated:
		return e.ThreadID, true
	case *types.TurnModerationMetadata:
		return e.ThreadID, true
	}
	return "", false
}

func threadIDFromItemEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.ItemStarted:
		return e.ThreadID, true
	case *types.ItemUpdated:
		return e.ThreadID, true
	case *types.ItemCompleted:
		return e.ThreadID, true
	case *types.FileChangePatchUpdated:
		return e.ThreadID, true
	case *types.ItemGuardianApprovalReviewStarted:
		return e.ThreadID, true
	case *types.ItemGuardianApprovalReviewCompleted:
		return e.ThreadID, true
	}
	return "", false
}

func threadIDFromRealtimeEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.ThreadRealtimeStarted:
		return e.ThreadID, true
	case *types.ThreadRealtimeClosed:
		return e.ThreadID, true
	case *types.ThreadRealtimeError:
		return e.ThreadID, true
	case *types.ThreadRealtimeItemAdded:
		return e.ThreadID, true
	case *types.ThreadRealtimeOutputAudioDelta:
		return e.ThreadID, true
	case *types.ThreadRealtimeSdp:
		return e.ThreadID, true
	case *types.ThreadRealtimeTranscriptDelta:
		return e.ThreadID, true
	case *types.ThreadRealtimeTranscriptDone:
		return e.ThreadID, true
	}
	return "", false
}

func threadIDFromMCPEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.MCPServerStartupStatusUpdated:
		if e.ThreadID != nil {
			return *e.ThreadID, true
		}
		return "", true
	case *types.MCPServerOAuthLoginCompleted:
		if e.ThreadID != nil {
			return *e.ThreadID, true
		}
		return "", true
	}
	return "", false
}

func threadIDFromMiscEvent(ev types.ThreadEvent) (string, bool) {
	switch e := ev.(type) {
	case *types.TokenUsageUpdated:
		return e.ThreadID, true
	case *types.HookStarted:
		return e.ThreadID, true
	case *types.HookCompleted:
		return e.ThreadID, true
	case *types.ModelRerouted:
		return e.ThreadID, true
	case *types.ModelVerification:
		return e.ThreadID, true
	case *types.ModelSafetyBufferingUpdated:
		return e.ThreadID, true
	case *types.Warning:
		if e.ThreadID != nil {
			return *e.ThreadID, true
		}
		return "", true
	case *types.GuardianWarning:
		return e.ThreadID, true
	case *types.ServerRequestResolved:
		return e.ThreadID, true
	case *types.UnknownEvent:
		return e.ThreadID, true
	}
	return "", false
}
