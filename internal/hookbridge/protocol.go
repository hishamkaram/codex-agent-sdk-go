// Package hookbridge is the SDK-side half of the codex-sdk-hook-shim
// bridge. The shim binary under cmd/codex-sdk-hook-shim/ dials this
// package's Unix socket listener, forwards the hook subprocess payload,
// and returns the SDK's decision as exit-code + stdout.
//
// Protocol is length-prefixed JSON frames (big-endian uint32 length):
//
//	Shim → SDK: HookRequest
//	SDK  → Shim: HookResponse
//
// The wire types here are duplicated from cmd/codex-sdk-hook-shim/main.go
// intentionally — the shim is a separate go-installable binary that
// MUST NOT depend on any other SDK package (keeps its install footprint
// minimal and avoids circular deps).
package hookbridge

// ShimVersion must match cmd/codex-sdk-hook-shim/main.go's ShimVersion.
// When the protocol evolves, both sides bump and the SDK checks.
const ShimVersion = "0.2.0"

// HookRequest is sent by the shim to the SDK. Mirrors the struct in
// cmd/codex-sdk-hook-shim/main.go. Keep the JSON tags identical across
// both files.
type HookRequest struct {
	ShimVersion string            `json:"shim_version"`
	Stdin       string            `json:"stdin"`
	Env         map[string]string `json:"env,omitempty"`
}

// HookResponse is returned by the SDK to the shim. Mirrors the struct in
// cmd/codex-sdk-hook-shim/main.go.
//
// ExitCode 0 = allow, 2 = block (codex's blocking convention). The shim
// writes Stdout to its stdout (codex reads this as the hook's stdout)
// and Stderr to its stderr (codex uses this for blocking reasons).
type HookResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// Hook output shape that codex expects on the subprocess's stdout for
// each hook event name. The SDK converts a HookDecision into one of
// these before wrapping it in HookResponse.Stdout.
//
// These structures duplicate the codex binary's HookSpecificOutput
// types. Reverse-engineered from binary strings (see docs/hooks.md).

// PreToolUseOutput is the JSON shape codex expects on stdout when the
// preToolUse hook writes to stdout.
//
//	{"hookSpecificOutput": {"hookEventName": "PreToolUse",
//	                        "permissionDecision": "allow|deny|ask",
//	                        "permissionDecisionReason": "...",
//	                        "updatedInput": {...}}}
type PreToolUseOutput struct {
	HookSpecificOutput PreToolUseHookSpecific `json:"hookSpecificOutput"`
	SystemMessage      string                 `json:"systemMessage,omitempty"`
	Continue           *bool                  `json:"continue,omitempty"`
}

// PreToolUseHookSpecific is the nested permission decision payload.
type PreToolUseHookSpecific struct {
	HookEventName            string `json:"hookEventName"` // always "PreToolUse"
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             any    `json:"updatedInput,omitempty"`
}

// PostToolUseOutput mirrors PreToolUseOutput for the postToolUse event.
type PostToolUseOutput struct {
	HookSpecificOutput PostToolUseHookSpecific `json:"hookSpecificOutput"`
	SystemMessage      string                  `json:"systemMessage,omitempty"`
	Continue           *bool                   `json:"continue,omitempty"`
}

// PostToolUseHookSpecific is the nested payload for postToolUse.
type PostToolUseHookSpecific struct {
	HookEventName     string `json:"hookEventName"` // always "PostToolUse"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SessionStartOutput is the hook output shape for sessionStart.
type SessionStartOutput struct {
	HookSpecificOutput SessionStartHookSpecific `json:"hookSpecificOutput"`
	SystemMessage      string                   `json:"systemMessage,omitempty"`
	Continue           *bool                    `json:"continue,omitempty"`
}

// SessionStartHookSpecific is the nested payload for sessionStart.
type SessionStartHookSpecific struct {
	HookEventName     string `json:"hookEventName"` // always "SessionStart"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// UserPromptSubmitOutput is the hook output shape for userPromptSubmit.
// When Continue is false or PermissionDecision is "deny", codex rejects
// the prompt.
type UserPromptSubmitOutput struct {
	HookSpecificOutput UserPromptSubmitHookSpecific `json:"hookSpecificOutput"`
	SystemMessage      string                       `json:"systemMessage,omitempty"`
	Continue           *bool                        `json:"continue,omitempty"`
	StopReason         string                       `json:"stopReason,omitempty"`
	SuppressOutput     bool                         `json:"suppressOutput,omitempty"`
}

// UserPromptSubmitHookSpecific is the nested payload for userPromptSubmit.
type UserPromptSubmitHookSpecific struct {
	HookEventName     string `json:"hookEventName"` // always "UserPromptSubmit"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// StopOutput is the hook output shape for the stop event. Setting
// Decision to "block" with Reason causes codex to continue the turn.
type StopOutput struct {
	Decision       string `json:"decision,omitempty"` // "approve" | "block"
	Reason         string `json:"reason,omitempty"`
	SystemMessage  string `json:"systemMessage,omitempty"`
	Continue       *bool  `json:"continue,omitempty"`
	StopReason     string `json:"stopReason,omitempty"`
	SuppressOutput bool   `json:"suppressOutput,omitempty"`
}
