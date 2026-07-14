package codex

import (
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func sp(s string) *string { return &s }

// TestExtractThreadIDFromEvent_Oracle is the behavior-equivalence safety net for
// the extractThreadIDFromEvent refactor (a 38-case type switch, cyclomatic 41,
// split into category helpers). Every event type the switch recognizes is
// constructed with ThreadID "T" and must extract "T"; a dropped or
// mis-categorized case regresses to "" and fails here. The *types.Warning
// nil-guard and a deliberately-unrecognized type pin the "" outcomes.
func TestExtractThreadIDFromEvent_Oracle(t *testing.T) {
	t.Parallel()

	const id = "T"
	cases := []struct {
		name string
		ev   types.ThreadEvent
		want string
	}{
		{"ThreadStarted", &types.ThreadStarted{ThreadID: id}, id},
		{"TurnStarted", &types.TurnStarted{ThreadID: id}, id},
		{"TurnCompleted", &types.TurnCompleted{ThreadID: id}, id},
		{"TurnFailed", &types.TurnFailed{ThreadID: id}, id},
		{"ItemStarted", &types.ItemStarted{ThreadID: id}, id},
		{"ItemUpdated", &types.ItemUpdated{ThreadID: id}, id},
		{"ItemCompleted", &types.ItemCompleted{ThreadID: id}, id},
		{"TokenUsageUpdated", &types.TokenUsageUpdated{ThreadID: id}, id},
		{"ContextCompacted", &types.ContextCompacted{ThreadID: id}, id},
		{"HookStarted", &types.HookStarted{ThreadID: id}, id},
		{"HookCompleted", &types.HookCompleted{ThreadID: id}, id},
		{"ThreadArchived", &types.ThreadArchived{ThreadID: id}, id},
		{"ThreadUnarchived", &types.ThreadUnarchived{ThreadID: id}, id},
		{"ThreadClosed", &types.ThreadClosed{ThreadID: id}, id},
		{"ThreadDeleted", &types.ThreadDeleted{ThreadID: id}, id},
		{"ThreadNameUpdated", &types.ThreadNameUpdated{ThreadID: id}, id},
		{"ThreadStatusChanged", &types.ThreadStatusChanged{ThreadID: id}, id},
		{"ThreadSettingsUpdated", &types.ThreadSettingsUpdated{ThreadID: id}, id},
		{"TurnDiffUpdated", &types.TurnDiffUpdated{ThreadID: id}, id},
		{"TurnPlanUpdated", &types.TurnPlanUpdated{ThreadID: id}, id},
		{"TurnModerationMetadata", &types.TurnModerationMetadata{ThreadID: id}, id},
		{"FileChangePatchUpdated", &types.FileChangePatchUpdated{ThreadID: id}, id},
		{"ThreadGoalUpdated", &types.ThreadGoalUpdated{ThreadID: id}, id},
		{"ThreadGoalCleared", &types.ThreadGoalCleared{ThreadID: id}, id},
		{"ItemGuardianApprovalReviewStarted", &types.ItemGuardianApprovalReviewStarted{ThreadID: id}, id},
		{"ItemGuardianApprovalReviewCompleted", &types.ItemGuardianApprovalReviewCompleted{ThreadID: id}, id},
		{"ModelRerouted", &types.ModelRerouted{ThreadID: id}, id},
		{"ModelVerification", &types.ModelVerification{ThreadID: id}, id},
		{"ModelSafetyBufferingUpdated", &types.ModelSafetyBufferingUpdated{ThreadID: id}, id},
		{"MCPServerStartupStatusUpdated", &types.MCPServerStartupStatusUpdated{ThreadID: sp(id)}, id},
		{"MCPServerOAuthLoginCompleted", &types.MCPServerOAuthLoginCompleted{ThreadID: sp(id)}, id},
		{"Warning_nonNil", &types.Warning{ThreadID: sp(id)}, id},
		{"Warning_nil", &types.Warning{}, ""},
		{"GuardianWarning", &types.GuardianWarning{ThreadID: id}, id},
		{"ServerRequestResolved", &types.ServerRequestResolved{ThreadID: id}, id},
		{"ThreadRealtimeStarted", &types.ThreadRealtimeStarted{ThreadID: id}, id},
		{"ThreadRealtimeClosed", &types.ThreadRealtimeClosed{ThreadID: id}, id},
		{"ThreadRealtimeError", &types.ThreadRealtimeError{ThreadID: id}, id},
		{"ThreadRealtimeItemAdded", &types.ThreadRealtimeItemAdded{ThreadID: id}, id},
		{"ThreadRealtimeOutputAudioDelta", &types.ThreadRealtimeOutputAudioDelta{ThreadID: id}, id},
		{"ThreadRealtimeSdp", &types.ThreadRealtimeSdp{ThreadID: id}, id},
		{"ThreadRealtimeTranscriptDelta", &types.ThreadRealtimeTranscriptDelta{ThreadID: id}, id},
		{"ThreadRealtimeTranscriptDone", &types.ThreadRealtimeTranscriptDone{ThreadID: id}, id},
		{"UnknownEvent", &types.UnknownEvent{ThreadID: id}, id},
		// Not in the switch — must extract "".
		{"SkillsChanged_excluded", &types.SkillsChanged{}, ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractThreadIDFromEvent(tc.ev); got != tc.want {
				t.Fatalf("extractThreadIDFromEvent(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
