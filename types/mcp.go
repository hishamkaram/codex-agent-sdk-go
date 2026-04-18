package types

// McpServerConfig describes a single Model Context Protocol server the
// codex session may call into. Configured via
// CodexOptions.WithMCPServers(...). The SDK delivers the full map to the
// codex app-server via `config/batchWrite` immediately after initialize.
type McpServerConfig interface {
	isMcpServerConfig()
	// Kind returns a string discriminator for debugging/logging.
	Kind() string
}

// McpStdioConfig spawns a local subprocess that speaks MCP over stdio.
// This is the most common config type.
type McpStdioConfig struct {
	Command              string            `json:"command"`
	Args                 []string          `json:"args,omitempty"`
	EnvironmentVariables map[string]string `json:"environment_variables,omitempty"`
	StartupTimeoutMs     int               `json:"startup_timeout_ms,omitempty"`
	ToolTimeoutMs        int               `json:"tool_timeout_ms,omitempty"`
	DefaultApprovalMode  ApprovalPolicy    `json:"default_tools_approval_mode,omitempty"`
}

func (McpStdioConfig) isMcpServerConfig() {}

// Kind returns "stdio".
func (McpStdioConfig) Kind() string { return "stdio" }

// McpStreamableHTTPConfig connects to a remote MCP server over streamable
// HTTP. Supports bearer-token and OAuth auth schemes.
type McpStreamableHTTPConfig struct {
	URL                 string            `json:"url"`
	AuthType            string            `json:"auth_type,omitempty"` // "bearer" | "oauth" | ""
	BearerToken         string            `json:"bearer_token,omitempty"`
	OAuthCallbackURI    string            `json:"oauth_callback_uri,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	StartupTimeoutMs    int               `json:"startup_timeout_ms,omitempty"`
	ToolTimeoutMs       int               `json:"tool_timeout_ms,omitempty"`
	DefaultApprovalMode ApprovalPolicy    `json:"default_tools_approval_mode,omitempty"`
}

func (McpStreamableHTTPConfig) isMcpServerConfig() {}

// Kind returns "http".
func (McpStreamableHTTPConfig) Kind() string { return "http" }

// McpServerStatusInfo is the runtime status of an MCP server after codex
// has attempted to start it. Returned by `mcpServerStatus/list`.
type McpServerStatusInfo struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"` // "connected" | "error" | "starting" | "stopped"
	ErrorText string   `json:"error,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}
