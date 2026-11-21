package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestClient_MCPServerConfigWire(t *testing.T) {
	t.Parallel()

	defaults := map[string]types.McpServerConfig{
		"local": types.McpStdioConfig{
			Command:              "local-mcp",
			Args:                 []string{"--stdio"},
			EnvironmentVariables: map[string]string{"SDK_MCP_SENTINEL": "config-only"},
			StartupTimeoutMs:     275,
			ToolTimeoutMs:        1250,
			DefaultApprovalMode:  types.ApprovalPolicy("prompt"),
		},
	}
	overrides := map[string]types.McpServerConfig{
		"remote": &types.McpStreamableHTTPConfig{
			URL:                 "https://mcp.example.test/path",
			AuthType:            "oauth",
			OAuthCallbackURI:    "http://127.0.0.1:12345/callback",
			Headers:             map[string]string{"X-MCP-Sentinel": "config-only"},
			DefaultApprovalMode: types.ApprovalPolicy("writes"),
		},
	}
	c, server := setupMockClient(t, types.NewCodexOptions().WithMCPServers(defaults).WithReasoningEffort("medium"),
		func(req jsonrpc.Request) jsonrpc.Response {
			switch req.Method {
			case "thread/start":
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"source"}}`)}
			case "thread/fork":
				return jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"fork"}}`)}
			default:
				return jsonrpc.Response{ID: req.ID, Error: &jsonrpc.RPCError{Code: -32601, Message: "unexpected method"}}
			}
		})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	source, err := c.StartThread(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ForkThread(ctx, source.ID(), &types.ThreadOptions{MCPServers: overrides}); err != nil {
		t.Fatal(err)
	}

	requests := observedThreadRequests(t, server)
	assertThreadRequestMethods(t, requests, "thread/start", "thread/fork")
	assertThreadConfigJSON(t, requests[0].Params, `{
		"sandbox":"read-only", "approvalPolicy":"on-request",
		"config":{
			"model_reasoning_effort":"medium",
			"mcp_servers":{"local":{
				"command":"local-mcp", "args":["--stdio"], "env":{"SDK_MCP_SENTINEL":"config-only"},
				"startup_timeout_ms":275, "tool_timeout_sec":1.25, "default_tools_approval_mode":"prompt"
			}}
		}
	}`)
	assertThreadConfigJSON(t, requests[1].Params, `{
		"threadId":"source", "sandbox":"read-only", "approvalPolicy":"on-request",
		"config":{
			"model_reasoning_effort":"medium",
			"mcp_servers":{"remote":{
				"url":"https://mcp.example.test/path", "auth":"oauth",
				"oauth":{"callback_url":"http://127.0.0.1:12345/callback"},
				"http_headers":{"X-MCP-Sentinel":"config-only"}, "default_tools_approval_mode":"writes"
			}}
		}
	}`)
}

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

func TestClient_MCPServerConfigRejectsBeforeDispatch(t *testing.T) {
	t.Parallel()

	type stdioWithHTTPSettings struct {
		types.McpStdioConfig
		Auth types.MCPHTTPAuth
	}

	const endpoint = "https://mcp.example.test"
	const secret = "synthetic-private-value"
	for _, tc := range []struct {
		name   string
		config types.McpServerConfig
		err    string
	}{
		{"typed nil", (*types.McpStdioConfig)(nil), "non-nil"},
		{"mixed transports", stdioWithHTTPSettings{McpStdioConfig: types.McpStdioConfig{Command: "local-mcp"}, Auth: types.MCPHTTPAuthOAuth}, "McpStdioConfig or McpStreamableHTTPConfig"},
		{"empty stdio command", types.McpStdioConfig{}, "Command"},
		{"negative timeout", types.McpStdioConfig{Command: "local-mcp", StartupTimeoutMs: -1}, "negative"},
		{"general Codex approval", types.McpStdioConfig{Command: "local-mcp", DefaultApprovalMode: types.ApprovalOnRequest}, "types.MCPToolApprovalPrompt"},
		{"invalid MCP approval", types.McpStdioConfig{Command: "local-mcp", ToolApprovalMode: types.MCPToolApprovalMode(secret)}, "approval mode"},
		{"conflicting approvals", types.McpStdioConfig{Command: "local-mcp", DefaultApprovalMode: types.ApprovalPolicy("auto"), ToolApprovalMode: types.MCPToolApprovalPrompt}, "conflicts"},
		{"raw token", types.McpStreamableHTTPConfig{URL: endpoint, BearerToken: secret}, "BearerTokenEnvVar"},
		{"malformed URL", types.McpStreamableHTTPConfig{URL: "https://mcp.example.test/" + secret + "%"}, "URL"},
		{"legacy bearer without env", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "bearer"}, "BearerTokenEnvVar"},
		{"legacy auth flow", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "chatgpt"}, "legacy AuthType"},
		{"canonical bearer auth", types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuth("bearer")}, "Auth"},
		{"ambiguous auth", types.McpStreamableHTTPConfig{URL: endpoint, AuthType: "oauth", Auth: types.MCPHTTPAuthOAuth}, "only one"},
		{"invalid env name", types.McpStreamableHTTPConfig{URL: endpoint, BearerTokenEnvVar: "TOKEN=" + secret}, "environment variable"},
		{"header and env", types.McpStreamableHTTPConfig{URL: endpoint, BearerTokenEnvVar: "SDK_TOKEN", Headers: map[string]string{"authorization": secret}}, "conflicting"},
		{"duplicate auth headers", types.McpStreamableHTTPConfig{URL: endpoint, Headers: map[string]string{"Authorization": secret, "authorization": secret}}, "conflicting"},
		{"EMA with alternate credentials", types.McpStreamableHTTPConfig{URL: endpoint, Auth: types.MCPHTTPAuthEMA, BearerTokenEnvVar: "SDK_TOKEN"}, "alternate credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &types.ThreadOptions{MCPServers: map[string]types.McpServerConfig{"invalid": tc.config}}
			c, server := setupMockClient(t, types.NewCodexOptions(), nil)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			_, startErr := c.StartThread(ctx, opts)
			_, _, forkErr := c.ForkThread(ctx, "source", opts)
			for _, err := range []error{startErr, forkErr} {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatal("invalid config error exposed a secret")
				}
			}
			assertThreadRequestMethods(t, observedThreadRequests(t, server))
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
