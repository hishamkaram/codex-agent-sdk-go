package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type threadConfigOverrides struct {
	webSearchMode   types.WebSearchMode
	reasoningEffort string
	mcpServers      map[string]types.McpServerConfig
	workspaceWrite  *workspaceWriteSettings
}

func (opts threadConfigOverrides) withDefaults(defaults *types.CodexOptions) threadConfigOverrides {
	if defaults != nil {
		opts.webSearchMode = cmp.Or(opts.webSearchMode, defaults.DefaultWebSearchMode)
		opts.reasoningEffort = cmp.Or(opts.reasoningEffort, defaults.DefaultReasoningEffort)
	}
	return opts
}

// buildThreadConfig returns a fresh request config. Callers supply MCP and
// workspace settings only on the methods that already own those overrides.
func buildThreadConfig(opts threadConfigOverrides) map[string]any {
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
	webSearchMode  types.WebSearchMode
}

// buildThreadStartParams builds the common start and fork request fields.
func buildThreadStartParams(defaults *types.CodexOptions, opts *types.ThreadOptions, appServerCwd string) (threadCreationRequest, error) {
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
	overrides := (threadConfigOverrides{
		webSearchMode:   opts.WebSearchMode,
		reasoningEffort: opts.ReasoningEffort,
		mcpServers:      servers,
		workspaceWrite:  workspaceWrite,
	}).withDefaults(defaults)
	if config := buildThreadConfig(overrides); config != nil {
		params["config"] = config
	}
	if workspaceWrite != nil {
		cwd, err = expectedThreadCwd(appServerCwd, cwd)
		if err != nil {
			return threadCreationRequest{}, err
		}
	}
	return threadCreationRequest{
		params: params, cwd: cwd, workspaceWrite: workspaceWrite, webSearchMode: overrides.webSearchMode,
	}, nil
}

// validateThreadWebSearchMode checks managed constraints before Codex can
// substitute its own allowed mode for the SDK's requested mode.
func (c *Client) validateThreadWebSearchMode(ctx context.Context, mode types.WebSearchMode) error {
	if mode == "" {
		return nil
	}
	requirements, err := c.ReadConfigRequirements(ctx)
	if err != nil {
		return err
	}
	if requirements == nil || requirements.AllowedWebSearchModes == nil {
		return nil
	}
	if !slices.Contains(requirements.AllowedWebSearchModes, mode) {
		return &types.WebSearchModeDisallowedError{Mode: mode}
	}
	return nil
}

func (r threadCreationRequest) responseCwd(result json.RawMessage, id string) (string, error) {
	if r.workspaceWrite == nil {
		return r.cwd, nil
	}
	return verifyWorkspaceWrite(result, id, r.cwd, r.workspaceWrite)
}

// verifyResumeReasoningEffort checks the provider's readback when available.
// Older peers may omit reasoningEffort. A reported null means no effective
// effort, so it also disagrees with a nonempty request.
func verifyResumeReasoningEffort(result json.RawMessage, threadID, expected string) error {
	if expected == "" {
		return nil
	}
	var reply struct {
		ReasoningEffort json.RawMessage `json:"reasoningEffort"`
	}
	if json.Unmarshal(result, &reply) != nil {
		return &types.ThreadResumeSettingsMismatchError{ThreadID: threadID}
	}
	if len(reply.ReasoningEffort) == 0 {
		return nil
	}
	var actual string
	if json.Unmarshal(reply.ReasoningEffort, &actual) != nil || actual != expected {
		return &types.ThreadResumeSettingsMismatchError{ThreadID: threadID}
	}
	return nil
}

// appServerWorkingDirectory reads the launch directory from the transport
// retained by this connection. Close may clear that transport concurrently.
func (c *Client) appServerWorkingDirectory() string {
	c.lifecycleMu.Lock()
	tr := c.tr
	c.lifecycleMu.Unlock()
	if tr == nil {
		return ""
	}
	return tr.WorkingDirectory()
}
