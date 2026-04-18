package hookbridge

import (
	"encoding/json"
	"fmt"
)

// HooksConfig is the hooks.json shape codex reads from CODEX_HOME.
// Reverse-engineered from codex binary strings (`"hooks"`, `"matcher"`,
// `"type":"command"`, `timeout`, `async`).
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

// HookHandlerConfig is a single handler. v0.2.0 SDK only generates
// "command" handlers — prompt/agent stay config-file-only.
type HookHandlerConfig struct {
	Type    string `json:"type"` // "command" | "prompt" | "agent"
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // milliseconds
	Async   bool   `json:"async,omitempty"`
}

// GenerateHooksJSON writes a hooks.json content pointing at the shim
// binary. shimPath MUST be an absolute path to codex-sdk-hook-shim. The
// generated config registers the shim for all five hook event names
// (preToolUse, postToolUse, sessionStart, userPromptSubmit, stop) with
// matcher "*" so every event flows through the SDK's callback.
//
// timeoutMs bounds how long codex waits for the hook subprocess to
// respond (shim dial + SDK callback + shim reply). Default 30_000.
func GenerateHooksJSON(shimPath string, timeoutMs int) ([]byte, error) {
	if shimPath == "" {
		return nil, fmt.Errorf("hookbridge.GenerateHooksJSON: shimPath must not be empty")
	}
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}
	handler := HookHandlerConfig{
		Type:    "command",
		Command: shimPath,
		Timeout: timeoutMs,
	}
	group := HookMatcherGroup{
		Matcher: "*",
		Hooks:   []HookHandlerConfig{handler},
	}
	cfg := HooksConfig{
		Hooks: map[string][]HookMatcherGroup{
			"preToolUse":       {group},
			"postToolUse":      {group},
			"sessionStart":     {group},
			"userPromptSubmit": {group},
			"stop":             {group},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
