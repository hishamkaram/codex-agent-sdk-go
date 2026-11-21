package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestExtractThreadID_NestedShape(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"thread":{"id":"T-nested"},"model":"gpt-5.4"}`)
	id, err := ThreadIDFromReply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id != "T-nested" {
		t.Fatalf("id = %q", id)
	}
}

func TestExtractThreadID_FlatShape(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"threadId":"T-flat"}`)
	id, _ := ThreadIDFromReply(raw)
	if id != "T-flat" {
		t.Fatalf("id = %q", id)
	}
}

func TestExtractThreadID_Missing(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"unrelated":"field"}`)
	_, err := ThreadIDFromReply(raw)
	if err == nil {
		t.Fatal("expected error for missing thread id")
	}
	if !types.IsMessageParseError(err) {
		t.Fatalf("expected MessageParseError, got %T", err)
	}
}

func TestExtractThreadIDFromEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ev   types.ThreadEvent
		want string
	}{
		{"ThreadStarted", &types.ThreadStarted{ThreadID: "T1"}, "T1"},
		{"TurnStarted", &types.TurnStarted{ThreadID: "T2"}, "T2"},
		{"TurnCompleted", &types.TurnCompleted{ThreadID: "T3"}, "T3"},
		{"TurnFailed", &types.TurnFailed{ThreadID: "T4"}, "T4"},
		{"ItemStarted", &types.ItemStarted{ThreadID: "T5"}, "T5"},
		{"ItemUpdated", &types.ItemUpdated{ThreadID: "T6"}, "T6"},
		{"ItemCompleted", &types.ItemCompleted{ThreadID: "T7"}, "T7"},
		{"TokenUsageUpdated", &types.TokenUsageUpdated{ThreadID: "T8"}, "T8"},
		{"ContextCompacted", &types.ContextCompacted{ThreadID: "T9"}, "T9"},
		{"ThreadSettingsUpdated", &types.ThreadSettingsUpdated{ThreadID: "T10"}, "T10"},
		{"TurnModerationMetadata", &types.TurnModerationMetadata{ThreadID: "T11"}, "T11"},
		{"ErrorEvent_no_thread_id", &types.ErrorEvent{}, ""},
		{"ErrorEvent_thread_id", &types.ErrorEvent{ThreadID: "T12"}, "T12"},
		{"UnknownEvent_no_thread_id", &types.UnknownEvent{Method: "x"}, ""},
		{"UnknownEvent_thread_id", &types.UnknownEvent{Method: "x", ThreadID: "T13"}, "T13"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, _ := ThreadIdentity(c.ev); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func sp(s string) *string { return &s }

// TestExtractThreadIDFromEvent_Oracle is the behavior-equivalence safety net for
// the extractThreadIDFromEvent refactor, split into category helpers. Every
// event type the switch recognizes is
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
		{"ErrorEvent", &types.ErrorEvent{ThreadID: id}, id},
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
			if got, _ := ThreadIdentity(tc.ev); got != tc.want {
				t.Fatalf("extractThreadIDFromEvent(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
