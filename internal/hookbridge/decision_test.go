package hookbridge

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestHookDecisionOutputFollowsEventContract(t *testing.T) {
	t.Parallel()
	contextText, message := "use local data", "fixture policy"
	allow := types.HookAllow{AdditionalContext: &contextText, SystemMessage: &message}
	deny := types.HookDeny{Reason: "policy", SuppressOutput: true, SystemMessage: &message}

	for _, tc := range []struct {
		name     string
		event    types.HookEventName
		decision types.HookDecision
		stdout   string
		stderr   string
		exitCode int
	}{
		{
			name: "pre-tool allow omits unsupported optional fields", event: types.HookPreToolUse, decision: allow,
		},
		{
			name: "post-tool allow", event: types.HookPostToolUse, decision: allow,
			stdout: `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"use local data"},"systemMessage":"fixture policy"}`,
		},
		{
			name: "session-start allow", event: types.HookSessionStart, decision: allow,
			stdout: `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"use local data"},"systemMessage":"fixture policy"}`,
		},
		{
			name: "prompt allow", event: types.HookUserPromptSubmit, decision: allow,
			stdout: `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"use local data"},"continue":true,"systemMessage":"fixture policy"}`,
		},
		{
			name: "stop allow", event: types.HookStop, decision: allow,
			stdout: `{"decision":"approve","continue":true,"systemMessage":"fixture policy"}`,
		},
		{
			name: "pre-tool deny adds system message", event: types.HookPreToolUse, decision: deny, stderr: "policy", exitCode: 2,
			stdout: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"policy"},"systemMessage":"fixture policy"}`,
		},
		{
			name: "prompt deny", event: types.HookUserPromptSubmit, decision: deny, stderr: "policy", exitCode: 2,
			stdout: `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit"},"continue":false,"stopReason":"policy","systemMessage":"fixture policy","suppressOutput":true}`,
		},
		{
			name: "stop deny", event: types.HookStop, decision: deny, stderr: "policy", exitCode: 2,
			stdout: `{"decision":"block","reason":"policy","systemMessage":"fixture policy","suppressOutput":true}`,
		},
		{
			name: "post-tool deny without JSON output", event: types.HookPostToolUse, decision: deny, stderr: "policy", exitCode: 2,
		},
		{
			name: "unknown event allows silently", event: types.HookEventName("FutureHook"), decision: allow,
		},
		{
			name: "unset callback result allows silently", event: types.HookStop,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DecisionToResponse(tc.event, tc.decision)
			if got.ExitCode != tc.exitCode || got.Stderr != tc.stderr {
				t.Errorf("exit, stderr = %d, %q, want %d, %q", got.ExitCode, got.Stderr, tc.exitCode, tc.stderr)
			}
			assertHookOutput(t, got.Stdout, tc.stdout)
		})
	}
}

func assertHookOutput(t *testing.T, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("stdout = %q, want empty", got)
		}
		return
	}
	var gotJSON, wantJSON any
	if err := json.Unmarshal([]byte(got), &gotJSON); err != nil {
		t.Fatalf("decode hook output %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatalf("decode expected hook output: %v", err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Errorf("stdout = %s, want %s", got, want)
	}
}
