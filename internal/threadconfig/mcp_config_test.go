package threadconfig

import (
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestEncodeMCPServerOptions(t *testing.T) {
	t.Parallel()

	const endpoint = "https://mcp.example.test"
	for _, tc := range []struct {
		name   string
		config types.McpServerConfig
		want   string
	}{
		{
			name:   "options omitted",
			config: &types.McpStdioConfig{Command: "local-mcp", EnvironmentVariables: map[string]string{}, Args: []string{}},
			want:   `{"command":"local-mcp"}`,
		},
		{
			name: "legacy bearer with env and typed approval",
			config: types.McpStreamableHTTPConfig{
				URL: endpoint, AuthType: "bearer", BearerTokenEnvVar: "SDK_MCP_TOKEN",
				ToolTimeoutMs: 1, ToolApprovalMode: types.MCPToolApprovalApprove,
				Headers: map[string]string{"X-MCP-Sentinel": "fixture"},
			},
			want: `{"url":"https://mcp.example.test","bearer_token_env_var":"SDK_MCP_TOKEN","tool_timeout_sec":0.001,"default_tools_approval_mode":"approve","http_headers":{"X-MCP-Sentinel":"fixture"}}`,
		},
		{
			name:   "typed OAuth",
			config: types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuthOAuth, ToolApprovalMode: types.MCPToolApprovalPrompt},
			want:   `{"url":"https://mcp.example.test","auth":"oauth","default_tools_approval_mode":"prompt"}`,
		},
		{
			name: "ChatGPT with explicit credential precedence and matching approval fields",
			config: types.McpStreamableHTTPConfig{
				URL: endpoint, Auth: types.MCPHTTPAuthChatGPT, BearerTokenEnvVar: "SDK_MCP_TOKEN",
				ToolApprovalMode: types.MCPToolApprovalAuto, DefaultApprovalMode: types.ApprovalPolicy("auto"),
			},
			want: `{"url":"https://mcp.example.test","auth":"chatgpt","bearer_token_env_var":"SDK_MCP_TOKEN","default_tools_approval_mode":"auto"}`,
		},
		{
			name:   "EMA",
			config: types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuthEMA, Headers: map[string]string{"X-Correlation-ID": "fixture"}},
			want:   `{"url":"https://mcp.example.test","auth":"ema_auth","http_headers":{"X-Correlation-ID":"fixture"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := encodeMCPServer(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			assertThreadConfigJSON(t, wire, tc.want)
		})
	}
}

func TestBuildMCPServerOverridesSnapshotsCallerSettings(t *testing.T) {
	t.Parallel()

	args := []string{"--stdio"}
	env := map[string]string{"SDK_MCP_SENTINEL": "original"}
	headers := map[string]string{"X-MCP-Sentinel": "original"}
	defaults := map[string]types.McpServerConfig{
		"stdio": types.McpStdioConfig{Command: "local-mcp", Args: args, EnvironmentVariables: env},
		"http":  types.McpStreamableHTTPConfig{URL: "https://mcp.example.test", Headers: headers},
	}

	wire, err := buildMCPServerOverrides(defaults, nil)
	if err != nil {
		t.Fatal(err)
	}
	args[0] = "--changed"
	env["SDK_MCP_SENTINEL"] = "changed"
	headers["X-MCP-Sentinel"] = "changed"
	delete(defaults, "stdio")

	assertThreadConfigJSON(t, wire, `{
		"stdio":{"command":"local-mcp","args":["--stdio"],"env":{"SDK_MCP_SENTINEL":"original"}},
		"http":{"url":"https://mcp.example.test","http_headers":{"X-MCP-Sentinel":"original"}}
	}`)
}
