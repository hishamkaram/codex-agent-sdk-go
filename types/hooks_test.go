package types

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHookDecisionMarkers(t *testing.T) {
	t.Parallel()
	// Compile-time: all three decisions satisfy HookDecision.
	var _ HookDecision = HookAllow{}
	var _ HookDecision = HookDeny{Reason: "x"}
	var _ HookDecision = HookAsk{}
}

func TestDefaultAllowHookHandler(t *testing.T) {
	t.Parallel()
	got := DefaultAllowHookHandler(context.Background(), HookInput{HookEventName: HookPreToolUse})
	if _, ok := got.(HookAllow); !ok {
		t.Fatalf("got %T, want HookAllow", got)
	}
}

func TestHookEventMethods(t *testing.T) {
	t.Parallel()
	if (&HookStarted{}).EventMethod() != "hook/started" {
		t.Fatal("HookStarted.EventMethod")
	}
	if (&HookCompleted{}).EventMethod() != "hook/completed" {
		t.Fatal("HookCompleted.EventMethod")
	}
}

func TestNormalizeHookEventName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   HookEventName
		want HookEventName
	}{
		{"PascalCase PreToolUse", "PreToolUse", HookPreToolUse},
		{"PascalCase PostToolUse", "PostToolUse", HookPostToolUse},
		{"PascalCase SessionStart", "SessionStart", HookSessionStart},
		{"PascalCase UserPromptSubmit", "UserPromptSubmit", HookUserPromptSubmit},
		{"PascalCase Stop", "Stop", HookStop},
		{"camelCase already-normalized preToolUse", HookPreToolUse, HookPreToolUse},
		{"camelCase already-normalized stop", HookStop, HookStop},
		{"unknown event passes through", "futureHook", HookEventName("futureHook")},
		{"empty passes through", "", HookEventName("")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeHookEventName(tt.in); got != tt.want {
				t.Fatalf("NormalizeHookEventName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHookInput_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		payload     string
		wantEvent   HookEventName
		wantSession string
		wantTool    string
		wantToolUse string
		wantModel   string
		wantCwd     string
	}{
		{
			name: "PascalCase PreToolUse with all fields populated (live shim payload shape)",
			payload: `{
				"session_id": "019da212-eb94",
				"turn_id": "019da212",
				"transcript_path": "/home/.codex/sessions/x.jsonl",
				"cwd": "/tmp/hook-probe",
				"hook_event_name": "PreToolUse",
				"model": "gpt-5.4",
				"permission_mode": "bypassPermissions",
				"tool_name": "Bash",
				"tool_input": {"command": "ls /tmp"},
				"tool_use_id": "call_GKgAC0yYcNYC2wKE0LDIJFaI"
			}`,
			wantEvent:   HookPreToolUse,
			wantSession: "019da212-eb94",
			wantTool:    "Bash",
			wantToolUse: "call_GKgAC0yYcNYC2wKE0LDIJFaI",
			wantModel:   "gpt-5.4",
			wantCwd:     "/tmp/hook-probe",
		},
		{
			name:        "camelCase userPromptSubmit (back-compat with shim test fixtures)",
			payload:     `{"hook_event_name": "userPromptSubmit", "session_id": "abc"}`,
			wantEvent:   HookUserPromptSubmit,
			wantSession: "abc",
		},
		{
			name:      "PascalCase Stop normalizes to stop",
			payload:   `{"hook_event_name": "Stop"}`,
			wantEvent: HookStop,
		},
		{
			name:      "Unknown PascalCase event passes through",
			payload:   `{"hook_event_name": "FutureHook"}`,
			wantEvent: HookEventName("FutureHook"),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var in HookInput
			if err := json.Unmarshal([]byte(tt.payload), &in); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if in.HookEventName != tt.wantEvent {
				t.Errorf("HookEventName = %q, want %q", in.HookEventName, tt.wantEvent)
			}
			if in.SessionID != tt.wantSession {
				t.Errorf("SessionID = %q, want %q", in.SessionID, tt.wantSession)
			}
			if in.ToolName != tt.wantTool {
				t.Errorf("ToolName = %q, want %q", in.ToolName, tt.wantTool)
			}
			if in.ToolUseID != tt.wantToolUse {
				t.Errorf("ToolUseID = %q, want %q", in.ToolUseID, tt.wantToolUse)
			}
			if in.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", in.Model, tt.wantModel)
			}
			if in.Cwd != tt.wantCwd {
				t.Errorf("Cwd = %q, want %q", in.Cwd, tt.wantCwd)
			}
		})
	}
}
