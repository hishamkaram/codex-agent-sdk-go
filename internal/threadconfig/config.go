package threadconfig

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

type threadConfigOverrides struct {
	webSearchMode   types.WebSearchMode
	reasoningEffort string
	mcpServers      map[string]mcpServerRequest
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

// CreationRequest couples the wire settings with the evidence required
// before the Client can register the returned thread.
type CreationRequest struct {
	Params         map[string]any
	cwd            string
	workspaceWrite *workspaceWriteSettings
	WebSearchMode  types.WebSearchMode
}

// BuildCreation builds the common start and fork request fields.
func BuildCreation(defaults *types.CodexOptions, opts *types.ThreadOptions, appServerCwd string) (CreationRequest, error) {
	if defaults == nil {
		defaults = &types.CodexOptions{}
	}
	if opts == nil {
		opts = &types.ThreadOptions{}
	}
	cwd := opts.Cwd
	if defaults.DefaultCwd != "" {
		// The default already positioned the app-server. Sending the
		// relative spelling again would resolve it a second time.
		cwd = cmp.Or(cwd, appServerCwd, defaults.DefaultCwd)
	}
	sandbox := cmp.Or(opts.Sandbox, defaults.DefaultSandbox)
	workspaceWrite, err := resolveWorkspaceWrite(sandbox, opts)
	if err != nil {
		return CreationRequest{}, err
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
		params["approvalPolicy"] = EncodeApprovalPolicy(policy)
	}
	if opts.ThreadSource != "" {
		params["threadSource"] = opts.ThreadSource
	}
	servers, err := buildMCPServerOverrides(defaults.DefaultMCPServers, opts.MCPServers)
	if err != nil {
		return CreationRequest{}, err
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
			return CreationRequest{}, err
		}
	}
	return CreationRequest{
		Params: params, cwd: cwd, workspaceWrite: workspaceWrite, WebSearchMode: overrides.webSearchMode,
	}, nil
}

// ResponseCwd checks the launch result against any requested workspace policy.
func (r CreationRequest) ResponseCwd(result json.RawMessage, id string) (string, error) {
	if r.workspaceWrite == nil {
		return r.cwd, nil
	}
	return verifyWorkspaceWrite(result, id, r.cwd, r.workspaceWrite)
}

// ResumeOverrides holds the configurable policies supported by thread/resume.
type ResumeOverrides struct {
	WebSearchMode   types.WebSearchMode
	ReasoningEffort string
}

// ResolveResumeOverrides applies the same defaults as thread creation.
func ResolveResumeOverrides(defaults *types.CodexOptions, opts *types.ResumeOptions) ResumeOverrides {
	overrides := (threadConfigOverrides{
		webSearchMode:   opts.WebSearchMode,
		reasoningEffort: opts.ReasoningEffort,
	}).withDefaults(defaults)
	return ResumeOverrides{WebSearchMode: overrides.webSearchMode, ReasoningEffort: overrides.reasoningEffort}
}

// Params builds the resume wire request with only supported overrides.
func (o ResumeOverrides) Params(threadID, cwd string) map[string]any {
	params := map[string]any{"threadId": threadID}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if config := buildThreadConfig(threadConfigOverrides{
		webSearchMode:   o.WebSearchMode,
		reasoningEffort: o.ReasoningEffort,
	}); config != nil {
		params["config"] = config
	}
	return params
}

// ValidateWebSearchMode checks an explicit mode against managed requirements.
func ValidateWebSearchMode(mode types.WebSearchMode, requirements *types.ConfigRequirements) error {
	if requirements == nil || requirements.AllowedWebSearchModes == nil {
		return nil
	}
	if !slices.Contains(requirements.AllowedWebSearchModes, mode) {
		return &types.WebSearchModeDisallowedError{Mode: mode}
	}
	return nil
}

// DecodeResumeModel identifies the saved model in a thread/read reply.
func DecodeResumeModel(result json.RawMessage, threadID string) (string, error) {
	var reply struct {
		Thread struct {
			Model string `json:"model"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &reply) != nil || strings.TrimSpace(reply.Thread.Model) == "" {
		return "", &types.ThreadResumeModelUnavailableError{ThreadID: threadID}
	}
	return reply.Thread.Model, nil
}

// VerifyResumeReasoningEffort checks the provider's readback when available.
// Older peers may omit reasoningEffort. A reported null means no effective
// effort, so it also disagrees with a nonempty request.
func VerifyResumeReasoningEffort(result json.RawMessage, threadID, expected string) error {
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
