package events

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// Real wire shape for hook/started, verified against the v2 schema:
//
//	{"threadId": "...", "turnId": "..."?, "run": HookRunSummary}
func TestParseHookStarted_RealShape(t *testing.T) {
	t.Parallel()
	raw := `{
		"threadId": "T1",
		"turnId": "U1",
		"run": {
			"id": "run-abc",
			"eventName": "preToolUse",
			"handlerType": "command",
			"executionMode": "sync",
			"scope": "turn",
			"sourcePath": "/home/u/.codex/hooks.json",
			"entries": [],
			"status": "running",
			"displayOrder": 1,
			"startedAt": 1776371820
		}
	}`
	ev, err := ParseEvent(jsonrpc.Notification{
		Method: "hook/started",
		Params: json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := ev.(*types.HookStarted)
	if hs.ThreadID != "T1" {
		t.Fatalf("ThreadID = %q", hs.ThreadID)
	}
	if hs.TurnID == nil || *hs.TurnID != "U1" {
		t.Fatalf("TurnID = %+v", hs.TurnID)
	}
	if hs.Run.ID != "run-abc" {
		t.Fatalf("Run.ID = %q", hs.Run.ID)
	}
	if hs.Run.EventName != types.HookPreToolUse {
		t.Fatalf("Run.EventName = %q", hs.Run.EventName)
	}
	if hs.Run.HandlerType != types.HookHandlerCommand {
		t.Fatalf("Run.HandlerType = %q", hs.Run.HandlerType)
	}
	if hs.Run.ExecutionMode != types.HookExecutionSync {
		t.Fatalf("Run.ExecutionMode = %q", hs.Run.ExecutionMode)
	}
	if hs.Run.Scope != types.HookScopeTurn {
		t.Fatalf("Run.Scope = %q", hs.Run.Scope)
	}
	if hs.Run.Status != types.HookRunStatusRunning {
		t.Fatalf("Run.Status = %q", hs.Run.Status)
	}
	if hs.Run.StartedAt != 1776371820 {
		t.Fatalf("Run.StartedAt = %d", hs.Run.StartedAt)
	}
}

// hook/completed: same shape as hook/started but status/completedAt/
// durationMs populated.
func TestParseHookCompleted_WithEntries(t *testing.T) {
	t.Parallel()
	raw := `{
		"threadId": "T1",
		"turnId": "U1",
		"run": {
			"id": "run-abc",
			"eventName": "preToolUse",
			"handlerType": "command",
			"executionMode": "sync",
			"scope": "turn",
			"sourcePath": "/home/u/.codex/hooks.json",
			"entries": [
				{"kind": "feedback", "text": "looks safe"},
				{"kind": "warning", "text": "this command is destructive"}
			],
			"status": "blocked",
			"statusMessage": "blocked by allowlist",
			"displayOrder": 1,
			"startedAt": 1776371820,
			"completedAt": 1776371823,
			"durationMs": 3000
		}
	}`
	ev, err := ParseEvent(jsonrpc.Notification{
		Method: "hook/completed",
		Params: json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	hc := ev.(*types.HookCompleted)
	if hc.Run.Status != types.HookRunStatusBlocked {
		t.Fatalf("Run.Status = %q", hc.Run.Status)
	}
	if hc.Run.StatusMessage == nil || *hc.Run.StatusMessage != "blocked by allowlist" {
		t.Fatalf("Run.StatusMessage = %+v", hc.Run.StatusMessage)
	}
	if hc.Run.CompletedAt == nil || *hc.Run.CompletedAt != 1776371823 {
		t.Fatalf("Run.CompletedAt = %+v", hc.Run.CompletedAt)
	}
	if hc.Run.DurationMs == nil || *hc.Run.DurationMs != 3000 {
		t.Fatalf("Run.DurationMs = %+v", hc.Run.DurationMs)
	}
	if len(hc.Run.Entries) != 2 {
		t.Fatalf("Entries = %d", len(hc.Run.Entries))
	}
	if hc.Run.Entries[0].Kind != types.HookOutputKindFeedback {
		t.Fatalf("Entry[0].Kind = %q", hc.Run.Entries[0].Kind)
	}
	if hc.Run.Entries[1].Kind != types.HookOutputKindWarning {
		t.Fatalf("Entry[1].Kind = %q", hc.Run.Entries[1].Kind)
	}
}

// hook events without turnId (sessionStart scope = thread, no active turn)
func TestParseHookStarted_NoTurnID(t *testing.T) {
	t.Parallel()
	raw := `{
		"threadId": "T1",
		"run": {
			"id": "run-sess",
			"eventName": "sessionStart",
			"handlerType": "command",
			"executionMode": "sync",
			"scope": "thread",
			"sourcePath": "/home/u/.codex/hooks.json",
			"entries": [],
			"status": "running",
			"displayOrder": 1,
			"startedAt": 1776371820
		}
	}`
	ev, _ := ParseEvent(jsonrpc.Notification{
		Method: "hook/started",
		Params: json.RawMessage(raw),
	})
	hs := ev.(*types.HookStarted)
	if hs.TurnID != nil {
		t.Fatalf("TurnID should be nil, got %+v", hs.TurnID)
	}
	if hs.Run.Scope != types.HookScopeThread {
		t.Fatalf("Scope = %q", hs.Run.Scope)
	}
}
