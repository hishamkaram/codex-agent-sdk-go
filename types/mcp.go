package types

// McpServerConfig describes a single Model Context Protocol server the
// codex session may call into. Configured via
// CodexOptions.WithMCPServers(...) or ThreadOptions.MCPServers. The SDK
// encodes the selected map as a per-thread `config.mcp_servers` override
// in `thread/start` and `thread/fork`.
type McpServerConfig interface {
	isMcpServerConfig()
	// Kind returns a string discriminator for debugging/logging.
	Kind() string
}

// MCPToolApprovalMode controls approval of MCP tools.
type MCPToolApprovalMode string

const (
	MCPToolApprovalAuto    MCPToolApprovalMode = "auto"
	MCPToolApprovalPrompt  MCPToolApprovalMode = "prompt"
	MCPToolApprovalWrites  MCPToolApprovalMode = "writes"
	MCPToolApprovalApprove MCPToolApprovalMode = "approve"
)

// MCPHTTPAuth selects an app-server authentication flow for an HTTP MCP server.
type MCPHTTPAuth string

const (
	MCPHTTPAuthOAuth   MCPHTTPAuth = "oauth"
	MCPHTTPAuthChatGPT MCPHTTPAuth = "chatgpt"
	MCPHTTPAuthEMA     MCPHTTPAuth = "ema_auth"
)

// McpStdioConfig spawns a local subprocess that speaks MCP over stdio.
// This is the most common config type.
type McpStdioConfig struct {
	Command              string              `json:"command"`
	Args                 []string            `json:"args,omitempty"`
	EnvironmentVariables map[string]string   `json:"environment_variables,omitempty"`
	StartupTimeoutMs     int                 `json:"startup_timeout_ms,omitempty"`
	ToolTimeoutMs        int                 `json:"tool_timeout_ms,omitempty"`
	ToolApprovalMode     MCPToolApprovalMode `json:"-"`
	// DefaultApprovalMode accepts native MCP approval values for compatibility.
	// Use ToolApprovalMode for new configurations.
	DefaultApprovalMode ApprovalPolicy `json:"default_tools_approval_mode,omitempty"`
}

func (McpStdioConfig) isMcpServerConfig() {}

// Kind returns "stdio".
func (McpStdioConfig) Kind() string { return "stdio" }

// McpStreamableHTTPConfig connects to an MCP server over streamable HTTP.
type McpStreamableHTTPConfig struct {
	URL  string      `json:"url"`
	Auth MCPHTTPAuth `json:"-"`
	// BearerTokenEnvVar names an environment variable available to the app-server.
	BearerTokenEnvVar string              `json:"-"`
	OAuthCallbackURI  string              `json:"oauth_callback_uri,omitempty"`
	Headers           map[string]string   `json:"headers,omitempty"`
	StartupTimeoutMs  int                 `json:"startup_timeout_ms,omitempty"`
	ToolTimeoutMs     int                 `json:"tool_timeout_ms,omitempty"`
	ToolApprovalMode  MCPToolApprovalMode `json:"-"`
	// AuthType accepts legacy OAuth and bearer selectors. Use Auth for OAuth.
	// For bearer authentication, set BearerTokenEnvVar and leave Auth empty.
	AuthType string `json:"auth_type,omitempty"`
	// BearerToken is retained for source compatibility. Thread requests reject
	// this field. Use BearerTokenEnvVar.
	BearerToken string `json:"bearer_token,omitempty"`
	// DefaultApprovalMode accepts native MCP approval values for compatibility.
	// Use ToolApprovalMode for new configurations.
	DefaultApprovalMode ApprovalPolicy `json:"default_tools_approval_mode,omitempty"`
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
