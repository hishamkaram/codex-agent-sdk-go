package codex

import "github.com/hishamkaram/codex-agent-sdk-go/types"

type threadConfigOverrides struct {
	webSearchMode   types.WebSearchMode
	reasoningEffort string
	mcpServers      map[string]types.McpServerConfig
	workspaceWrite  *workspaceWriteSettings
}

// buildThreadConfig returns a fresh request config. Callers supply MCP and
// workspace settings only on the methods that already own those overrides.
func buildThreadConfig(defaults *types.CodexOptions, opts threadConfigOverrides) map[string]any {
	if defaults != nil {
		if opts.webSearchMode == "" {
			opts.webSearchMode = defaults.DefaultWebSearchMode
		}
		if opts.reasoningEffort == "" {
			opts.reasoningEffort = defaults.DefaultReasoningEffort
		}
	}

	config := make(map[string]any)
	if opts.webSearchMode != "" {
		config["web_search"] = opts.webSearchMode
	}
	if opts.reasoningEffort != "" {
		config["model_reasoning_effort"] = opts.reasoningEffort
	}
	if len(opts.mcpServers) > 0 {
		config["mcp_servers"] = opts.mcpServers
	}
	if opts.workspaceWrite != nil {
		config["sandbox_workspace_write"] = opts.workspaceWrite
	}
	if len(config) == 0 {
		return nil
	}
	return config
}
