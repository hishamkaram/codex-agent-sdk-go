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

func TestBuildTurnInput_WithSkill(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{Skills: []types.SkillInput{
		{Name: "openai-docs", Path: "/abs/openai-docs/SKILL.md"},
	}}
	got, err := buildTurnInput("$openai-docs explain responses", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0]["type"] != "text" || got[0]["text"] != "$openai-docs explain responses" {
		t.Fatalf("text: %+v", got[0])
	}
	if got[1]["type"] != "skill" || got[1]["name"] != "openai-docs" || got[1]["path"] != "/abs/openai-docs/SKILL.md" {
		t.Fatalf("skill: %+v", got[1])
	}
}

func TestBuildTurnInput_WithSkillAndImage(t *testing.T) {
	t.Parallel()
	opts := &types.RunOptions{
		Skills: []types.SkillInput{{Name: "imagegen", Path: "/abs/imagegen/SKILL.md"}},
		Images: []string{"/abs/a.png"},
	}
	got, err := buildTurnInput("$imagegen use this", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[1]["type"] != "skill" || got[1]["name"] != "imagegen" {
		t.Fatalf("skill: %+v", got[1])
	}
	if got[2]["type"] != "localImage" || got[2]["path"] != "/abs/a.png" {
		t.Fatalf("image: %+v", got[2])
	}
}

func TestBuildTurnInput_EmptySkillFieldsAreErrors(t *testing.T) {
	t.Parallel()
	if _, err := buildTurnInput("x", &types.RunOptions{Skills: []types.SkillInput{{Path: "/abs/SKILL.md"}}}); err == nil {
		t.Fatal("expected error for empty skill name")
	}
	if _, err := buildTurnInput("x", &types.RunOptions{Skills: []types.SkillInput{{Name: "x"}}}); err == nil {
		t.Fatal("expected error for empty skill path")
	}
}

func TestBuildTurnInput_EmptyImagePathIsError(t *testing.T) {
	t.Parallel()
	_, err := buildTurnInput("x", &types.RunOptions{Images: []string{""}})
	if err == nil {
		t.Fatal("expected error for empty image path")
	}
}

func TestBuildTurnStartParams_WithCollaborationMode(t *testing.T) {
	t.Parallel()
	input := []map[string]any{{"type": "text", "text": "make a plan"}}
	opts := &types.RunOptions{
		CollaborationMode: &types.CollaborationMode{
			Mode: types.CollaborationModePlan,
			Settings: types.CollaborationModeSettings{
				Model: "gpt-5.4",
			},
		},
	}

	got := buildTurnStartParams("thread-1", input, opts)
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %v", got["threadId"])
	}
	if got["input"] == nil {
		t.Fatalf("input missing: %+v", got)
	}
	cm, ok := got["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode = %T", got["collaborationMode"])
	}
	if cm.Mode != types.CollaborationModePlan || cm.Settings.Model != "gpt-5.4" {
		t.Fatalf("collaborationMode = %+v", cm)
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

func TestExtractThreadModel(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"thread":{"id":"thread-1"},"model":"provider-model"}`)
	if got := extractThreadModel(raw); got != "provider-model" {
		t.Fatalf("extractThreadModel() = %q, want provider-model", got)
	}
}

func TestBuildThreadStartParams_DefaultMCPServers(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"local": types.McpStdioConfig{Command: "mcp-local", Args: []string{"--stdio"}},
	}
	p := buildThreadStartParams(
		types.NewCodexOptions().
			WithModel("gpt-5.4").
			WithCwd("/repo").
			WithSandbox(types.SandboxReadOnly).
			WithApprovalPolicy(types.ApprovalOnRequest).
			WithMCPServers(defaultServers),
		nil,
	)
	if p["model"] != "gpt-5.4" || p["cwd"] != "/repo" {
		t.Fatalf("model/cwd changed while adding MCP config: %+v", p)
	}
	if p["sandbox"] != string(types.SandboxReadOnly) || p["approvalPolicy"] != string(types.ApprovalOnRequest) {
		t.Fatalf("sandbox/approval changed while adding MCP config: %+v", p)
	}

	cfg, ok := p["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %T, want map[string]any; params=%+v", p["config"], p)
	}
	got, ok := cfg["mcp_servers"].(map[string]types.McpServerConfig)
	if !ok {
		t.Fatalf("mcp_servers = %T, want map[string]types.McpServerConfig", cfg["mcp_servers"])
	}
	if got["local"].Kind() != "stdio" {
		t.Fatalf("local kind = %q, want stdio", got["local"].Kind())
	}
}

func TestBuildThreadStartParams_PerThreadMCPServersOverrideDefaults(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"default": types.McpStdioConfig{Command: "default-mcp"},
	}
	threadServers := map[string]types.McpServerConfig{
		"thread": types.McpStreamableHTTPConfig{URL: "http://127.0.0.1:8080/mcp"},
	}
	p := buildThreadStartParams(
		types.NewCodexOptions().WithMCPServers(defaultServers),
		&types.ThreadOptions{MCPServers: threadServers},
	)

	cfg := p["config"].(map[string]any)
	got := cfg["mcp_servers"].(map[string]types.McpServerConfig)
	if _, ok := got["default"]; ok {
		t.Fatalf("default MCP server leaked into per-thread override: %+v", got)
	}
	if got["thread"].Kind() != "http" {
		t.Fatalf("thread kind = %q, want http", got["thread"].Kind())
	}
}

func TestBuildThreadStartParams_EmptyThreadMCPServersPreserveDefaults(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"default": types.McpStdioConfig{Command: "default-mcp"},
	}
	p := buildThreadStartParams(
		types.NewCodexOptions().WithMCPServers(defaultServers),
		&types.ThreadOptions{MCPServers: map[string]types.McpServerConfig{}},
	)

	cfg := p["config"].(map[string]any)
	got := cfg["mcp_servers"].(map[string]types.McpServerConfig)
	if got["default"].Kind() != "stdio" {
		t.Fatalf("default kind = %q, want stdio", got["default"].Kind())
	}
}

func TestBuildThreadStartParams_GranularApprovalPolicyUsesStructuredObject(t *testing.T) {
	t.Parallel()

	p1 := buildThreadStartParams(&types.CodexOptions{DefaultApprovalPolicy: types.ApprovalGranular}, nil)
	assertGranularApprovalPolicyValue(t, p1["approvalPolicy"])

	p2 := buildThreadStartParams(
		&types.CodexOptions{DefaultApprovalPolicy: types.ApprovalOnRequest},
		&types.ThreadOptions{ApprovalPolicy: types.ApprovalGranular},
	)
	assertGranularApprovalPolicyValue(t, p2["approvalPolicy"])
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
		{"ContextCompacted", &types.ContextCompacted{ThreadID: "T9"}, "T9"},
		{"ThreadSettingsUpdated", &types.ThreadSettingsUpdated{ThreadID: "T10"}, "T10"},
		{"TurnModerationMetadata", &types.TurnModerationMetadata{ThreadID: "T11"}, "T11"},
		{"ErrorEvent_no_thread_id", &types.ErrorEvent{}, ""},
		{"UnknownEvent_no_thread_id", &types.UnknownEvent{Method: "x"}, ""},
		{"UnknownEvent_thread_id", &types.UnknownEvent{Method: "x", ThreadID: "T12"}, "T12"},
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
