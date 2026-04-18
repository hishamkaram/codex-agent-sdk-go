package hookbridge

import (
	"encoding/json"
	"fmt"
)

// HooksConfig is the hooks.json shape codex reads from ~/.codex/hooks.json.
// Verified live against codex 0.121.0 — see the v0.3.0 plan for the spike
// that nailed down the four wire-shape dimensions (PascalCase event keys,
// `.*` matcher, shell-string command, timeout in seconds).
type HooksConfig struct {
	Hooks map[string][]HookMatcherGroup `json:"hooks"`
}

// HookMatcherGroup is a set of handlers that fire when codex matches the
// matcher pattern against the hook's subject (typically a tool name or
// command).
type HookMatcherGroup struct {
	Matcher string              `json:"matcher"`
	Hooks   []HookHandlerConfig `json:"hooks"`
}

// HookHandlerConfig is a single handler. The SDK only generates "command"
// handlers — prompt/agent stay config-file-only. Command is a single
// shell-string (binary path + inline args, NOT a separate args array);
// codex passes it to its shell-runner. Timeout is in SECONDS, not ms.
type HookHandlerConfig struct {
	Type    string `json:"type"` // "command" | "prompt" | "agent"
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // seconds
	Async   bool   `json:"async,omitempty"`
}

// DefaultTimeoutSeconds is the default per-hook timeout codex enforces
// when none is specified in hooks.json. Matches the spike-verified value.
const DefaultTimeoutSeconds = 30

// GenerateHooksJSON returns the bytes of a hooks.json that registers the
// shim binary for every event name codex fires through the app-server
// transport. shimPath MUST be an absolute path. timeoutSeconds bounds how
// long codex waits for the shim subprocess to respond; <=0 picks the
// 30-second default.
//
// The generated config uses PascalCase event keys ("PreToolUse",
// "PostToolUse", "SessionStart", "UserPromptSubmit", "Stop"), the `.*`
// matcher (matches all tool names / prompts / sources), and a single
// shell-string command. These three dimensions match what codex 0.121.0
// actually accepts — earlier shapes (camelCase, `*` matcher, separate
// args array, timeout in ms) caused codex to silently no-op the hooks
// even when no parse error surfaced.
//
// Note: SessionStart is included for completeness, though it does NOT
// fire under the app-server transport (it's TUI-only — startup/resume/
// clear flows). The SDK includes it so that future codex versions which
// extend SessionStart to app-server flows pick it up automatically.
func GenerateHooksJSON(shimPath string, timeoutSeconds int) ([]byte, error) {
	if shimPath == "" {
		return nil, fmt.Errorf("hookbridge.GenerateHooksJSON: shimPath must not be empty")
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}
	handler := HookHandlerConfig{
		Type:    "command",
		Command: shimPath,
		Timeout: timeoutSeconds,
	}
	group := HookMatcherGroup{
		Matcher: ".*",
		Hooks:   []HookHandlerConfig{handler},
	}
	cfg := HooksConfig{
		Hooks: map[string][]HookMatcherGroup{
			"PreToolUse":       {group},
			"PostToolUse":      {group},
			"SessionStart":     {group},
			"UserPromptSubmit": {group},
			"Stop":             {group},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
