package codex

import (
	"cmp"
	"encoding/json"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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

// threadCreationRequest couples the wire settings with the evidence required
// before the Client can register the returned thread.
type threadCreationRequest struct {
	params         map[string]any
	cwd            string
	workspaceWrite *workspaceWriteSettings
}

// buildThreadStartParams builds the common start and fork request fields.
func buildThreadStartParams(defaults *types.CodexOptions, opts *types.ThreadOptions) (threadCreationRequest, error) {
	if defaults == nil {
		defaults = &types.CodexOptions{}
	}
	if opts == nil {
		opts = &types.ThreadOptions{}
	}
	cwd := cmp.Or(opts.Cwd, defaults.DefaultCwd)
	sandbox := cmp.Or(opts.Sandbox, defaults.DefaultSandbox)
	workspaceWrite, err := resolveWorkspaceWrite(sandbox, opts)
	if err != nil {
		return threadCreationRequest{}, err
	}

	params := map[string]any{}
	if model := cmp.Or(opts.Model, defaults.DefaultModel); model != "" {
		params["model"] = model
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if sandbox != "" {
		params["sandbox"] = string(sandbox)
	}
	if policy := cmp.Or(opts.ApprovalPolicy, defaults.DefaultApprovalPolicy); policy != "" {
		params["approvalPolicy"] = encodeApprovalPolicy(policy)
	}
	if opts.ThreadSource != "" {
		params["threadSource"] = opts.ThreadSource
	}
	servers := opts.MCPServers
	if len(servers) == 0 {
		servers = defaults.DefaultMCPServers
	}
	if config := buildThreadConfig(defaults, threadConfigOverrides{
		webSearchMode:   opts.WebSearchMode,
		reasoningEffort: opts.ReasoningEffort,
		mcpServers:      servers,
		workspaceWrite:  workspaceWrite,
	}); config != nil {
		params["config"] = config
	}
	if workspaceWrite != nil {
		cwd, err = expectedThreadCwd(defaults.DefaultCwd, cwd)
		if err != nil {
			return threadCreationRequest{}, err
		}
	}
	return threadCreationRequest{params: params, cwd: cwd, workspaceWrite: workspaceWrite}, nil
}

func (r threadCreationRequest) responseCwd(result json.RawMessage, id string) (string, error) {
	if r.workspaceWrite == nil {
		return r.cwd, nil
	}
	return verifyWorkspaceWrite(result, id, r.cwd, r.workspaceWrite)
}
