package threadconfig

import (
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func requireThreadStartParams(t *testing.T, defaults *types.CodexOptions, opts *types.ThreadOptions) map[string]any {
	t.Helper()
	request, err := BuildCreation(defaults, opts, "")
	if err != nil {
		t.Fatal(err)
	}
	return request.Params
}

func TestBuildThreadStartParams_DefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	clientOpts := types.NewCodexOptions().
		WithModel("gpt-5.4").
		WithCwd("/default/cwd").
		WithSandbox(types.SandboxReadOnly).
		WithApprovalPolicy(types.ApprovalOnRequest)

	// No per-call overrides — should use client defaults.
	p1 := requireThreadStartParams(t, clientOpts, nil)
	if p1["model"] != "gpt-5.4" || p1["cwd"] != "/default/cwd" {
		t.Fatalf("defaults: %+v", p1)
	}
	if p1["sandbox"] != string(types.SandboxReadOnly) {
		t.Fatalf("sandbox default: %v", p1["sandbox"])
	}

	// Per-call overrides — should win.
	p2 := requireThreadStartParams(t, clientOpts, &types.ThreadOptions{
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

func TestBuildThreadStartParams_DefaultMCPServers(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"local": types.McpStdioConfig{Command: "mcp-local", Args: []string{"--stdio"}},
	}
	p := requireThreadStartParams(t,
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

	assertThreadConfigJSON(t, p["config"], `{"mcp_servers":{"local":{"command":"mcp-local","args":["--stdio"]}}}`)
}

func TestBuildThreadStartParams_PerThreadMCPServersOverrideDefaults(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"default": types.McpStdioConfig{Command: "default-mcp"},
	}
	threadServers := map[string]types.McpServerConfig{
		"thread": types.McpStreamableHTTPConfig{URL: "http://127.0.0.1:8080/mcp"},
	}
	p := requireThreadStartParams(t,
		types.NewCodexOptions().WithMCPServers(defaultServers),
		&types.ThreadOptions{MCPServers: threadServers},
	)

	assertThreadConfigJSON(t, p["config"], `{"mcp_servers":{"thread":{"url":"http://127.0.0.1:8080/mcp"}}}`)
}

func TestBuildThreadStartParams_EmptyThreadMCPServersPreserveDefaults(t *testing.T) {
	t.Parallel()

	defaultServers := map[string]types.McpServerConfig{
		"default": types.McpStdioConfig{Command: "default-mcp"},
	}
	p := requireThreadStartParams(t,
		types.NewCodexOptions().WithMCPServers(defaultServers),
		&types.ThreadOptions{MCPServers: map[string]types.McpServerConfig{}},
	)

	assertThreadConfigJSON(t, p["config"], `{"mcp_servers":{"default":{"command":"default-mcp"}}}`)
}

func TestBuildThreadStartParams_GranularApprovalPolicyUsesStructuredObject(t *testing.T) {
	t.Parallel()

	p1 := requireThreadStartParams(t, &types.CodexOptions{DefaultApprovalPolicy: types.ApprovalGranular}, nil)
	assertGranularApprovalPolicyValue(t, p1["approvalPolicy"])

	p2 := requireThreadStartParams(t,
		&types.CodexOptions{DefaultApprovalPolicy: types.ApprovalOnRequest},
		&types.ThreadOptions{ApprovalPolicy: types.ApprovalGranular},
	)
	assertGranularApprovalPolicyValue(t, p2["approvalPolicy"])
}

func TestBuildThreadStartParams_EmptyClientOptsNoKeys(t *testing.T) {
	t.Parallel()
	clientOpts := &types.CodexOptions{} // no defaults
	p := requireThreadStartParams(t, clientOpts, nil)
	if len(p) != 0 {
		t.Fatalf("expected empty params, got %+v", p)
	}
}
