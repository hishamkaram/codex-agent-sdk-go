package codex

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/commands"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/hookbridge"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// ReadConfig returns codex's effective merged configuration view —
// the result of system + user + project layers + session overrides.
//
// The returned Config exposes a curated subset of well-known fields
// (Model, ApprovalPolicy, Sandbox, Features, etc.) plus a Raw map
// holding every key codex serialized, for forward compatibility.
//
// Read-only — safe to call concurrently with running turns. Maps to
// the slash-command-equivalent of /status (config portion) and
// /debug-config.
func (c *Client) ReadConfig(ctx context.Context) (*types.Config, error) {
	return commands.ReadConfig(ctx, c.sendRaw)
}

// ReadConfigRequirements returns provider-managed runtime constraints. A nil
// result means no requirements are configured.
func (c *Client) ReadConfigRequirements(ctx context.Context) (*types.ConfigRequirements, error) {
	return commands.ReadConfigRequirements(ctx, c.sendRaw)
}

// ErrModelPagination indicates that the model inventory returned a repeated
// cursor or exceeded the SDK's pagination bound.
var ErrModelPagination = errors.New("codex model pagination invalid")

// ListModels returns the complete model inventory for the provider's default
// filters, in server order. It follows up to 100 pages and returns nil data
// if pagination fails. Use ListModelsPage to control pagination or filters.
func (c *Client) ListModels(ctx context.Context) ([]types.ModelInfo, error) {
	return commands.ListModels(ctx, c.sendRaw, ErrModelPagination)
}

// ListModelsPage requests one page of models. Pass the previous page's
// NextCursor and the same filter options to continue.
func (c *Client) ListModelsPage(ctx context.Context, opts *types.ModelListOptions) (*types.ModelListResult, error) {
	return commands.ListModelsPage(ctx, c.sendRaw, opts)
}

// ListExperimentalFeatures returns every feature flag the user can
// toggle via SetExperimentalFeature (Batch 3). Each row carries the
// current Enabled state, DefaultEnabled, and lifecycle Stage.
//
// Read-only. Maps to the data source for slash command /experimental.
func (c *Client) ListExperimentalFeatures(ctx context.Context) ([]types.ExperimentalFeature, error) {
	return commands.ListExperimentalFeatures(ctx, c.sendRaw)
}

// ListMCPServerStatus returns the first page of the runtime state of configured
// MCP servers. Use ListMCPServerStatusPage with its NextCursor to continue.
func (c *Client) ListMCPServerStatus(ctx context.Context) (*types.MCPServerStatusListResult, error) {
	return commands.ListMCPServerStatus(ctx, c.sendRaw)
}

// ListMCPServerStatusPage requests one page of MCP server statuses. Pass the
// previous page's NextCursor and the same filter options to continue.
func (c *Client) ListMCPServerStatusPage(ctx context.Context, opts *types.MCPServerStatusListOptions) (*types.MCPServerStatusListResult, error) {
	return commands.ListMCPServerStatusPage(ctx, c.sendRaw, opts)
}

// ListApps returns the connectors / apps available to the current
// principal. Maps to slash command /apps.
//
// CAVEAT: in codex 0.121.0 with ChatGPT auth, the upstream
// chatgpt.com endpoint frequently returns HTTP 403 Forbidden,
// surfacing as an *types.RPCError with code -32603. Callers should
// inspect the returned error and degrade gracefully (e.g., hide the
// /apps surface in their UI).
func (c *Client) ListApps(ctx context.Context) ([]types.AppInfo, error) {
	return commands.ListApps(ctx, c.sendRaw)
}

// ListSkills returns every skill discovered across system, user, and
// project scopes. Codex returns skills GROUPED BY discovery directory
// — each top-level entry covers one cwd's worth of skills, plus any
// parse errors encountered for SKILL.md files in that directory.
//
// To get a flat list, concatenate Skills across every group. Most
// callers keep the grouping to display per-scope breakdowns matching
// the TUI.
//
// Read-only. Maps to data source for slash command /plugins.
func (c *Client) ListSkills(ctx context.Context) ([]types.SkillsCwdGroup, error) {
	var cwd string
	if c.opts != nil {
		cwd = c.opts.DefaultCwd
	}
	return commands.ListSkills(ctx, c.sendRaw, cwd)
}

// ReadAccount returns the authenticated principal's identity (auth
// type, email when applicable, plan tier).
//
// Read-only. Maps to the account portion of slash command /status.
func (c *Client) ReadAccount(ctx context.Context) (*types.AccountReadResult, error) {
	return commands.ReadAccount(ctx, c.sendRaw)
}

// ReadRateLimits returns a snapshot of the principal's API rate-limit
// usage. Codex returns BOTH a legacy single-bucket view
// (RateLimits) and a multi-bucket view keyed by limit ID
// (RateLimitsByLimitID). Prefer the per-limit map when present.
//
// Read-only. Maps to the usage portion of slash command /status.
func (c *Client) ReadRateLimits(ctx context.Context) (*types.RateLimitsReadResult, error) {
	return commands.ReadRateLimits(ctx, c.sendRaw)
}

// GetAuthStatus returns the SDK's view of the auth handshake — the
// auth method (chatgpt/apikey), the live token, and whether codex
// requires upstream OpenAI auth. Maps to the auth portion of slash
// command /status.
//
// SECURITY: AuthToken is a live JWT or API key. Do not log or
// transmit it. Callers should treat AuthStatus as a local-process
// secret and never persist it.
func (c *Client) GetAuthStatus(ctx context.Context) (*types.AuthStatus, error) {
	return commands.GetAuthStatus(ctx, c.sendRaw)
}

// WriteConfigValue persists one key/value pair to the codex config
// file (defaults to ~/.codex/config.toml). The keyPath is dotted
// (e.g., "model", "approval_policy", "features.tool_search"); the
// value is JSON-marshaled directly. The strategy controls how the
// write blends with any existing value (defaults to MergeReplace).
//
// Wire shape (verified live + against codex 0.121.0 schema):
//
//	{"keyPath": "...", "mergeStrategy": "replace", "value": <any>}
//
// Returns metadata about the write (status, file path, content
// hash) — useful for verifying the change persisted.
//
// Maps to the data-side of slash commands /model, /permissions,
// /personality, /fast, /statusline, /title.
func (c *Client) WriteConfigValue(ctx context.Context, keyPath string, value any, strategy types.MergeStrategy) (*types.ConfigWriteResponse, error) {
	return commands.WriteConfigValue(ctx, c.sendRaw, keyPath, value, strategy, c.rejectHookConfigWrite)
}

// WriteConfigBatch applies multiple config edits atomically. Codex
// performs the writes as a single transaction — either all succeed
// or none do. Each edit may carry its own MergeStrategy; if zero,
// MergeReplace is substituted before sending.
//
// Wire shape (verified live + against codex 0.121.0 schema):
//
//	{"edits": [{"keyPath": "...", "mergeStrategy": "replace", "value": ...}, ...]}
func (c *Client) WriteConfigBatch(ctx context.Context, edits []types.ConfigEntry) (*types.ConfigWriteResponse, error) {
	return commands.WriteConfigBatch(ctx, c.sendRaw, edits, c.rejectHookConfigWrite)
}

func (c *Client) rejectHookConfigWrite(callerName, keyPath string) error {
	if !hookbridge.IsHooksConfigKeyPath(keyPath) {
		return nil
	}
	if c == nil || c.opts == nil || c.opts.HookCallback == nil {
		return nil
	}
	if !c.session.Connected() || c.session.Closed() {
		return nil
	}
	return fmt.Errorf("codex.Client.%s: refusing to write %q while WithHookCallback owns generated hooks.json; close this client or create one without WithHookCallback", callerName, keyPath)
}

// SetModel is sugar for `WriteConfigValue("model", model, MergeReplace)`.
// Mid-session model switching survives subsequent turns.
func (c *Client) SetModel(ctx context.Context, model string) error {
	return commands.SetModel(ctx, c.sendRaw, model, c.rejectHookConfigWrite)
}

// SetReasoningEffort is sugar for
// `WriteConfigValue("model_reasoning_effort", effort, MergeReplace)`.
// Valid effort values are model-dependent; discover them from
// ModelInfo.SupportedReasoningEfforts instead of hard-coding support.
func (c *Client) SetReasoningEffort(ctx context.Context, effort string) error {
	return commands.SetReasoningEffort(ctx, c.sendRaw, effort, c.rejectHookConfigWrite)
}

// SetApprovalPolicy is sugar for
// `WriteConfigValue("approval_policy", encodedPolicy, MergeReplace)`.
// Use to change codex's approval stance mid-session (e.g., switch from
// `untrusted` to `never` after the user gains confidence). The public
// policy remains string-typed; the SDK encodes codex's structured
// granular variant internally.
func (c *Client) SetApprovalPolicy(ctx context.Context, policy types.ApprovalPolicy) error {
	return commands.SetApprovalPolicy(ctx, c.sendRaw, policy, c.rejectHookConfigWrite, c.validateApprovalPolicy)
}

// ErrSandboxConfigOverridden means the provider reported that the configured
// sandbox default was overridden. The requested value may have been saved.
var ErrSandboxConfigOverridden = errors.New("codex overrode the requested sandbox mode, and the config may have been saved")

// SetSandbox writes the configured sandbox default (sandbox_mode). ReadConfig
// can inspect the effective configuration. Codex resolves a new thread's
// policy from the configured default and applicable overrides.
func (c *Client) SetSandbox(ctx context.Context, sandbox types.SandboxMode) error {
	return commands.SetSandbox(ctx, c.sendRaw, sandbox, c.rejectHookConfigWrite, ErrSandboxConfigOverridden)
}

// SetExperimentalFeature toggles one experimental feature flag.
// Sugar around SetExperimentalFeatures for the common single-feature
// case. Equivalent to TUI `/experimental` selecting a feature and
// pressing enter. To list available features, call
// ListExperimentalFeatures.
func (c *Client) SetExperimentalFeature(ctx context.Context, name string, enabled bool) error {
	return commands.SetExperimentalFeature(ctx, c.sendRaw, name, enabled)
}

// SetExperimentalFeatures toggles multiple experimental feature flags
// in a single RPC. Pass an empty map for a no-op probe.
//
// Wire shape (verified against codex 0.121.0 schema):
//
//	{"enablement": {"shell_tool": true, "tool_search": false}}
//
// Omitted features are left unchanged (codex semantics, NOT cleared).
func (c *Client) SetExperimentalFeatures(ctx context.Context, enablement map[string]bool) error {
	return commands.SetExperimentalFeatures(ctx, c.sendRaw, enablement)
}

// Logout signs out of the current authentication mode. For ChatGPT
// auth this invalidates ~/.codex/auth.json — a subsequent codex call
// will require interactive `codex login`. For OPENAI_API_KEY auth
// this is effectively a no-op (the env var is still set).
//
// CAVEAT (verified live): after Logout, the subprocess MAY remain
// alive but downstream RPCs that require auth will fail. Callers
// should call Close after Logout if they intend to switch auth modes.
//
// Maps to slash command /logout.
func (c *Client) Logout(ctx context.Context) error {
	return commands.Logout(ctx, c.sendRaw)
}

// UploadFeedback submits a feedback report to OpenAI. Maps to slash
// command /feedback.
//
// PRIVACY: when report.IncludeLogs is true, codex bundles recent
// transcript logs (user prompts + assistant responses) with the
// upload. The SDK logs a WARN-level entry when this flag is set so
// callers know the implication.
//
// Codex's response carries only the thread ID the report was scoped
// to.
func (c *Client) UploadFeedback(ctx context.Context, report types.FeedbackReport) (*types.FeedbackReceipt, error) {
	return commands.UploadFeedback(ctx, c.sendRaw, report, func(classification string) {
		c.logger.Warn("UploadFeedback: IncludeLogs=true — recent thread transcripts will be uploaded to OpenAI",
			zap.String("classification", classification))
	})
}

var (
	// ErrBackgroundTerminalPagination marks a cursor cycle or a response that
	// exceeds the SDK's finite pagination bound.
	ErrBackgroundTerminalPagination    = errors.New("codex background terminal pagination invalid")
	ErrBackgroundTerminalNotTerminated = errors.New("codex background terminal was not terminated")
)

// ListBackgroundTerminals returns a complete live inventory for one provider
// thread. Pagination is bounded and cursor cycles fail closed.
func (c *Client) ListBackgroundTerminals(ctx context.Context, threadID string) ([]types.BackgroundTerminal, error) {
	return commands.ListBackgroundTerminals(ctx, c.sendRaw, threadID, ErrBackgroundTerminalPagination)
}

// TerminateBackgroundTerminal asks the provider to terminate exactly one
// process in one thread. A nil error is an acknowledgement, not lifecycle
// evidence; callers that expose state must wait for a fresh inventory without
// the process or a correlated terminal event.
func (c *Client) TerminateBackgroundTerminal(ctx context.Context, threadID, processID string) error {
	return commands.TerminateBackgroundTerminal(ctx, c.sendRaw, threadID, processID, ErrBackgroundTerminalNotTerminated)
}

// CleanBackgroundTerminals asks the provider to stop every background terminal
// in one thread. A nil error is an acknowledgement; callers must observe a
// fresh empty inventory before reporting that the terminals stopped.
func (c *Client) CleanBackgroundTerminals(ctx context.Context, threadID string) error {
	return commands.CleanBackgroundTerminals(ctx, c.sendRaw, threadID)
}
