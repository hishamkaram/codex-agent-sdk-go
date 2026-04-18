package hookbridge

import (
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// DecisionToResponse converts a user HookDecision into the HookResponse
// format the shim forwards to codex. The response's Stdout is the JSON
// codex expects on the hook subprocess's stdout (shape varies per hook
// event); ExitCode is 0 for allow, 2 for deny/block (codex's blocking
// convention).
//
// event identifies which hook fired — determines which output shape to
// use (PreToolUseOutput vs PostToolUseOutput vs …).
func DecisionToResponse(event types.HookEventName, decision types.HookDecision) HookResponse {
	switch d := decision.(type) {
	case types.HookAllow:
		return allowResponse(event, d)
	case types.HookDeny:
		return denyResponse(event, d)
	case types.HookAsk:
		return askResponse(event, d)
	default:
		// Unknown decision type: default to allow, log nothing (nothing
		// to log to on this side).
		return HookResponse{ExitCode: 0}
	}
}

func allowResponse(event types.HookEventName, a types.HookAllow) HookResponse {
	resp := HookResponse{ExitCode: 0}
	if a.SystemMessage != nil {
		// Common field across most output shapes — include it in a way
		// the unknown-event fallback still works.
	}
	out, err := buildAllowOutput(event, a)
	if err != nil {
		return HookResponse{Stdout: "", ExitCode: 0, Stderr: err.Error()}
	}
	if out != "" {
		resp.Stdout = out
	}
	return resp
}

func buildAllowOutput(event types.HookEventName, a types.HookAllow) (string, error) {
	switch event {
	case types.HookPreToolUse:
		out := PreToolUseOutput{
			HookSpecificOutput: PreToolUseHookSpecific{
				HookEventName:      "PreToolUse",
				PermissionDecision: "allow",
			},
		}
		if a.SystemMessage != nil {
			out.SystemMessage = *a.SystemMessage
		}
		if len(a.UpdatedInput) > 0 {
			var updated any
			if err := json.Unmarshal(a.UpdatedInput, &updated); err != nil {
				return "", err
			}
			out.HookSpecificOutput.UpdatedInput = updated
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookPostToolUse:
		out := PostToolUseOutput{
			HookSpecificOutput: PostToolUseHookSpecific{HookEventName: "PostToolUse"},
		}
		if a.AdditionalContext != nil {
			out.HookSpecificOutput.AdditionalContext = *a.AdditionalContext
		}
		if a.SystemMessage != nil {
			out.SystemMessage = *a.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookSessionStart:
		out := SessionStartOutput{
			HookSpecificOutput: SessionStartHookSpecific{HookEventName: "SessionStart"},
		}
		if a.AdditionalContext != nil {
			out.HookSpecificOutput.AdditionalContext = *a.AdditionalContext
		}
		if a.SystemMessage != nil {
			out.SystemMessage = *a.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookUserPromptSubmit:
		trueVal := true
		out := UserPromptSubmitOutput{
			HookSpecificOutput: UserPromptSubmitHookSpecific{HookEventName: "UserPromptSubmit"},
			Continue:           &trueVal,
		}
		if a.AdditionalContext != nil {
			out.HookSpecificOutput.AdditionalContext = *a.AdditionalContext
		}
		if a.SystemMessage != nil {
			out.SystemMessage = *a.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookStop:
		trueVal := true
		out := StopOutput{
			Decision: "approve",
			Continue: &trueVal,
		}
		if a.SystemMessage != nil {
			out.SystemMessage = *a.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	}
	// Unknown event — no stdout, allow by exit 0 convention.
	return "", nil
}

func denyResponse(event types.HookEventName, d types.HookDeny) HookResponse {
	resp := HookResponse{ExitCode: 2, Stderr: d.Reason}
	out, err := buildDenyOutput(event, d)
	if err != nil {
		return HookResponse{ExitCode: 2, Stderr: d.Reason}
	}
	if out != "" {
		resp.Stdout = out
	}
	return resp
}

func buildDenyOutput(event types.HookEventName, d types.HookDeny) (string, error) {
	switch event {
	case types.HookPreToolUse:
		out := PreToolUseOutput{
			HookSpecificOutput: PreToolUseHookSpecific{
				HookEventName:            "PreToolUse",
				PermissionDecision:       "deny",
				PermissionDecisionReason: d.Reason,
			},
		}
		if d.SystemMessage != nil {
			out.SystemMessage = *d.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookUserPromptSubmit:
		falseVal := false
		out := UserPromptSubmitOutput{
			HookSpecificOutput: UserPromptSubmitHookSpecific{HookEventName: "UserPromptSubmit"},
			Continue:           &falseVal,
			StopReason:         d.Reason,
			SuppressOutput:     d.SuppressOutput,
		}
		if d.SystemMessage != nil {
			out.SystemMessage = *d.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	case types.HookStop:
		out := StopOutput{
			Decision:       "block",
			Reason:         d.Reason,
			SuppressOutput: d.SuppressOutput,
		}
		if d.SystemMessage != nil {
			out.SystemMessage = *d.SystemMessage
		}
		b, err := json.Marshal(out)
		return string(b), err
	}
	// PostToolUse / SessionStart don't support deny semantics — exit 2
	// with reason on stderr still blocks in practice.
	return "", nil
}

func askResponse(event types.HookEventName, a types.HookAsk) HookResponse {
	if event != types.HookPreToolUse {
		// "ask" only meaningful for preToolUse; allow otherwise.
		return HookResponse{ExitCode: 0}
	}
	out := PreToolUseOutput{
		HookSpecificOutput: PreToolUseHookSpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "ask",
			PermissionDecisionReason: a.Reason,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return HookResponse{ExitCode: 0}
	}
	return HookResponse{Stdout: string(b), ExitCode: 0}
}
