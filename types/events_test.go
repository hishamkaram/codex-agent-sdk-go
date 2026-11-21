package types

import "testing"

func TestEventMethod_EveryKnownEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		event ThreadEvent
		want  string
	}{
		{&ThreadStarted{}, "thread/started"},
		{&TurnStarted{}, "turn/started"},
		{&TurnCompleted{}, "turn/completed"},
		{&TurnFailed{}, "turn/failed"},
		{&ItemStarted{}, "item/started"},
		{&ItemUpdated{}, "item/updated"},
		{&ItemCompleted{}, "item/completed"},
		{&TokenUsageUpdated{}, "thread/tokenUsage/updated"},
		{&ContextCompacted{}, "thread/compacted"},
		{&ErrorEvent{}, "error"},
		{&ThreadArchived{}, "thread/archived"},
		{&ThreadUnarchived{}, "thread/unarchived"},
		{&ThreadClosed{}, "thread/closed"},
		{&ThreadDeleted{}, "thread/deleted"},
		{&ThreadNameUpdated{}, "thread/name/updated"},
		{&ThreadStatusChanged{}, "thread/status/changed"},
		{&ThreadSettingsUpdated{}, "thread/settings/updated"},
		{&ThreadGoalUpdated{}, "thread/goal/updated"},
		{&ThreadGoalCleared{}, "thread/goal/cleared"},
		{&TurnDiffUpdated{}, "turn/diff/updated"},
		{&TurnPlanUpdated{}, "turn/plan/updated"},
		{&TurnModerationMetadata{}, "turn/moderationMetadata"},
		{&ItemGuardianApprovalReviewStarted{}, "item/autoApprovalReview/started"},
		{&ItemGuardianApprovalReviewCompleted{}, "item/autoApprovalReview/completed"},
		{&FileChangePatchUpdated{}, "item/fileChange/patchUpdated"},
		{&MCPServerStartupStatusUpdated{}, "mcpServer/startupStatus/updated"},
		{&MCPServerOAuthLoginCompleted{}, "mcpServer/oauthLogin/completed"},
		{&AccountLoginCompleted{}, "account/login/completed"},
		{&AccountRateLimitsUpdated{}, "account/rateLimits/updated"},
		{&AccountUpdated{}, "account/updated"},
		{&ModelRerouted{}, "model/rerouted"},
		{&ModelVerification{}, "model/verification"},
		{&ModelSafetyBufferingUpdated{}, "model/safetyBuffering/updated"},
		{&ConfigWarning{}, "configWarning"},
		{&DeprecationNotice{}, "deprecationNotice"},
		{&Warning{}, "warning"},
		{&GuardianWarning{}, "guardianWarning"},
		{&FsChanged{}, "fs/changed"},
		{&SkillsChanged{}, "skills/changed"},
		{&AppListUpdated{}, "app/list/updated"},
		{&ServerRequestResolved{}, "serverRequest/resolved"},
		{&ProcessOutputDelta{}, "process/outputDelta"},
		{&ProcessExited{}, "process/exited"},
		{&CommandExecOutputDelta{}, "command/exec/outputDelta"},
		{&RemoteControlStatusChanged{}, "remoteControl/status/changed"},
		{&ExternalAgentConfigImportCompleted{}, "externalAgentConfig/import/completed"},
		{&ExternalAgentConfigImportProgress{}, "externalAgentConfig/import/progress"},
		{&WindowsWorldWritableWarning{}, "windows/worldWritableWarning"},
		{&WindowsSandboxSetupCompleted{}, "windowsSandbox/setupCompleted"},
		{&FuzzyFileSearchSessionUpdated{}, "fuzzyFileSearch/sessionUpdated"},
		{&FuzzyFileSearchSessionCompleted{}, "fuzzyFileSearch/sessionCompleted"},
		{&ThreadRealtimeStarted{}, "thread/realtime/started"},
		{&ThreadRealtimeClosed{}, "thread/realtime/closed"},
		{&ThreadRealtimeError{}, "thread/realtime/error"},
		{&ThreadRealtimeItemAdded{}, "thread/realtime/itemAdded"},
		{&ThreadRealtimeOutputAudioDelta{}, "thread/realtime/outputAudio/delta"},
		{&ThreadRealtimeSdp{}, "thread/realtime/sdp"},
		{&ThreadRealtimeTranscriptDelta{}, "thread/realtime/transcript/delta"},
		{&ThreadRealtimeTranscriptDone{}, "thread/realtime/transcript/done"},
		{&UnknownEvent{Method: "future/event"}, "future/event"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.event.EventMethod(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
