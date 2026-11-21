package codex

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// mcpServerRequest is the app-server config shape. It owns the snapshot
// of one transport's settings for a single thread creation request.
type mcpServerRequest struct {
	mcpCommonRequest
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	URL               string            `json:"url,omitempty"`
	Auth              types.MCPHTTPAuth `json:"auth,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty"`
	Headers           map[string]string `json:"http_headers,omitempty"`
	OAuth             *mcpOAuthRequest  `json:"oauth,omitempty"`
}

type mcpCommonRequest struct {
	StartupTimeoutMs int                       `json:"startup_timeout_ms,omitempty"`
	ToolTimeoutSec   float64                   `json:"tool_timeout_sec,omitempty"`
	ToolApprovalMode types.MCPToolApprovalMode `json:"default_tools_approval_mode,omitempty"`
}

type mcpOAuthRequest struct {
	CallbackURL string `json:"callback_url"`
}

func buildMCPServerOverrides(defaults, overrides map[string]types.McpServerConfig) (map[string]mcpServerRequest, error) {
	if len(overrides) == 0 {
		overrides = defaults
	}
	if len(overrides) == 0 {
		return nil, nil
	}
	servers := make(map[string]mcpServerRequest, len(overrides))
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		config, err := encodeMCPServer(overrides[name])
		if err != nil {
			return nil, fmt.Errorf("MCP server %q: %w", name, err)
		}
		servers[name] = config
	}
	return servers, nil
}

// Accept the SDK's two concrete transports, in value or pointer form.
// In particular, an embedded type cannot introduce foreign wire fields.
func encodeMCPServer(server types.McpServerConfig) (mcpServerRequest, error) {
	switch config := server.(type) {
	case types.McpStdioConfig:
		return encodeMCPStdio(config)
	case *types.McpStdioConfig:
		if config != nil {
			return encodeMCPStdio(*config)
		}
	case types.McpStreamableHTTPConfig:
		return encodeMCPHTTP(config)
	case *types.McpStreamableHTTPConfig:
		if config != nil {
			return encodeMCPHTTP(*config)
		}
	}
	return mcpServerRequest{}, errors.New("expected a non-nil McpStdioConfig or McpStreamableHTTPConfig")
}

func encodeMCPStdio(config types.McpStdioConfig) (mcpServerRequest, error) {
	if strings.TrimSpace(config.Command) == "" {
		return mcpServerRequest{}, errors.New("stdio Command must not be empty")
	}
	common, err := encodeMCPCommon(config.StartupTimeoutMs, config.ToolTimeoutMs, config.ToolApprovalMode, config.DefaultApprovalMode)
	if err != nil {
		return mcpServerRequest{}, err
	}
	return mcpServerRequest{
		mcpCommonRequest: common,
		Command:          config.Command,
		Args:             slices.Clone(config.Args),
		Env:              maps.Clone(config.EnvironmentVariables),
	}, nil
}

func encodeMCPHTTP(config types.McpStreamableHTTPConfig) (mcpServerRequest, error) {
	if config.BearerToken != "" {
		return mcpServerRequest{}, errors.New("BearerToken is unsupported, use BearerTokenEnvVar")
	}
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return mcpServerRequest{}, errors.New("URL must be an absolute HTTP or HTTPS URL")
	}
	auth, err := resolveMCPHTTPAuth(config)
	if err != nil {
		return mcpServerRequest{}, err
	}
	if credentialErr := validateMCPCredentials(config, auth, endpoint.User != nil); credentialErr != nil {
		return mcpServerRequest{}, credentialErr
	}
	common, err := encodeMCPCommon(config.StartupTimeoutMs, config.ToolTimeoutMs, config.ToolApprovalMode, config.DefaultApprovalMode)
	if err != nil {
		return mcpServerRequest{}, err
	}
	wire := mcpServerRequest{
		mcpCommonRequest:  common,
		URL:               config.URL,
		Auth:              auth,
		BearerTokenEnvVar: config.BearerTokenEnvVar,
		Headers:           maps.Clone(config.Headers),
	}
	if config.OAuthCallbackURI != "" {
		wire.OAuth = &mcpOAuthRequest{CallbackURL: config.OAuthCallbackURI}
	}
	return wire, nil
}

func encodeMCPCommon(startupMs, toolMs int, mode types.MCPToolApprovalMode, legacy types.ApprovalPolicy) (mcpCommonRequest, error) {
	if startupMs < 0 || toolMs < 0 {
		return mcpCommonRequest{}, errors.New("StartupTimeoutMs and ToolTimeoutMs must not be negative")
	}
	if legacy != "" && mode != "" && types.MCPToolApprovalMode(legacy) != mode {
		return mcpCommonRequest{}, errors.New("ToolApprovalMode conflicts with DefaultApprovalMode")
	}
	if mode == "" {
		mode = types.MCPToolApprovalMode(legacy)
	}
	switch mode {
	case "", types.MCPToolApprovalAuto, types.MCPToolApprovalPrompt, types.MCPToolApprovalWrites, types.MCPToolApprovalApprove:
		return mcpCommonRequest{
			StartupTimeoutMs: startupMs,
			ToolTimeoutSec:   float64(toolMs) / 1000,
			ToolApprovalMode: mode,
		}, nil
	default:
		return mcpCommonRequest{}, errors.New("invalid MCP tool approval mode, set ToolApprovalMode to auto, prompt (types.MCPToolApprovalPrompt), writes, or approve")
	}
}

func resolveMCPHTTPAuth(config types.McpStreamableHTTPConfig) (types.MCPHTTPAuth, error) {
	if config.Auth != "" && config.AuthType != "" {
		return "", errors.New("set only one of Auth and AuthType")
	}
	switch config.AuthType {
	case "":
		switch config.Auth {
		case "", types.MCPHTTPAuthOAuth, types.MCPHTTPAuthChatGPT, types.MCPHTTPAuthEMA:
			return config.Auth, nil
		default:
			return "", errors.New("invalid MCP HTTP Auth, choose oauth, chatgpt, or ema_auth")
		}
	case "oauth":
		return types.MCPHTTPAuthOAuth, nil
	case "bearer":
		if config.BearerTokenEnvVar == "" {
			return "", errors.New("bearer authentication requires BearerTokenEnvVar")
		}
		// Bearer is a credential source, not an app-server auth flow.
		return "", nil
	default:
		return "", errors.New("invalid legacy AuthType, use Auth or BearerTokenEnvVar")
	}
}

func validateMCPCredentials(config types.McpStreamableHTTPConfig, auth types.MCPHTTPAuth, urlCredentials bool) error {
	sources := 0
	if urlCredentials {
		sources++
	}
	if config.BearerTokenEnvVar != "" {
		if strings.TrimSpace(config.BearerTokenEnvVar) == "" || strings.ContainsAny(config.BearerTokenEnvVar, "=\x00") {
			return errors.New("BearerTokenEnvVar must name an environment variable")
		}
		sources++
	}
	for name := range config.Headers {
		if strings.EqualFold(name, "Authorization") {
			sources++
		}
	}
	if auth == types.MCPHTTPAuthEMA && sources != 0 {
		return errors.New("ema_auth cannot be combined with alternate credentials")
	}
	if sources > 1 {
		return errors.New("conflicting MCP HTTP credentials, choose one of BearerTokenEnvVar, an Authorization header, or URL credentials")
	}
	return nil
}
