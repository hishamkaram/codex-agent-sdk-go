package codex

import (
	"encoding/json"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestBuildTurnInput_TextOnly(t *testing.T) {
	t.Parallel()
	got, err := buildTurnInput("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0]["type"] != "text" || got[0]["text"] != "hello" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestBuildTurnInput_WithImages(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{Images: []string{"/abs/a.png", "/abs/b.jpg"}}
	got, err := buildTurnInput("describe these", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[1]["type"] != "localImage" || got[1]["path"] != "/abs/a.png" {
		t.Fatalf("image 0: %+v", got[1])
	}
	if got[2]["type"] != "localImage" || got[2]["path"] != "/abs/b.jpg" {
		t.Fatalf("image 1: %+v", got[2])
	}
}

func TestBuildTurnInput_EmptyImagePathIsError(t *testing.T) {
	t.Parallel()
	_, err := buildTurnInput("x", &types.RunOptions{Images: []string{""}})
	if err == nil {
		t.Fatal("expected error for empty image path")
	}
}

func TestBuildThreadStartParams_DefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	clientOpts := types.NewCodexOptions().
		WithModel("gpt-5.4").
		WithCwd("/default/cwd").
		WithSandbox(types.SandboxReadOnly).
		WithApprovalPolicy(types.ApprovalOnRequest)

	// No per-call overrides — should use client defaults.
	p1 := buildThreadStartParams(clientOpts, nil)
	if p1["model"] != "gpt-5.4" || p1["cwd"] != "/default/cwd" {
		t.Fatalf("defaults: %+v", p1)
	}
	if p1["sandbox"] != string(types.SandboxReadOnly) {
		t.Fatalf("sandbox default: %v", p1["sandbox"])
	}

	// Per-call overrides — should win.
	p2 := buildThreadStartParams(clientOpts, &types.ThreadOptions{
		Model:          "gpt-5.3-codex",
		Cwd:            "/override",
		Sandbox:        types.SandboxWorkspaceWrite,
		ApprovalPolicy: types.ApprovalUntrusted,
	})
	if p2["model"] != "gpt-5.3-codex" || p2["cwd"] != "/override" {
		t.Fatalf("override: %+v", p2)
	}
	if p2["sandbox"] != string(types.SandboxWorkspaceWrite) {
		t.Fatalf("sandbox override: %v", p2["sandbox"])
	}
	if p2["approvalPolicy"] != string(types.ApprovalUntrusted) {
		t.Fatalf("policy override: %v", p2["approvalPolicy"])
	}
}

func TestBuildThreadStartParams_EmptyClientOptsNoKeys(t *testing.T) {
	t.Parallel()
	clientOpts := &types.CodexOptions{} // no defaults
	p := buildThreadStartParams(clientOpts, nil)
	if len(p) != 0 {
		t.Fatalf("expected empty params, got %+v", p)
	}
}

func TestExtractThreadID_NestedShape(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"thread":{"id":"T-nested"},"model":"gpt-5.4"}`)
	id, err := extractThreadID(raw)
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
	id, _ := extractThreadID(raw)
	if id != "T-flat" {
		t.Fatalf("id = %q", id)
	}
}

func TestExtractThreadID_Missing(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"unrelated":"field"}`)
	_, err := extractThreadID(raw)
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
		{"CompactionEvent", &types.CompactionEvent{ThreadID: "T9"}, "T9"},
		{"ErrorEvent_no_thread_id", &types.ErrorEvent{}, ""},
		{"UnknownEvent_no_thread_id", &types.UnknownEvent{Method: "x"}, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := extractThreadIDFromEvent(c.ev); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsTurnTerminus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		ev       types.ThreadEvent
		expected string
		want     bool
	}{
		{"completed_matching_id", &types.TurnCompleted{TurnID: "U1"}, "U1", true},
		{"completed_mismatched_id", &types.TurnCompleted{TurnID: "U1"}, "U2", false},
		{"completed_empty_expected", &types.TurnCompleted{TurnID: "U1"}, "", true},
		{"failed_matching_id", &types.TurnFailed{TurnID: "U1"}, "U1", true},
		{"failed_empty_expected", &types.TurnFailed{TurnID: "U1"}, "", true},
		{"not_terminus", &types.ItemStarted{TurnID: "U1"}, "U1", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := isTurnTerminus(c.ev, c.expected); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
