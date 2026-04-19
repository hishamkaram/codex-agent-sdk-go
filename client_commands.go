package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// ====================================================================
// Read-only client methods (Batch 2 of v0.4.0).
//
// Each maps a slash-command-equivalent app-server JSON-RPC method:
//
//   ReadConfig                 → config/read                (≈/status, /debug-config)
//   ListModels                 → model/list                 (≈/model picker source)
//   ListExperimentalFeatures   → experimentalFeature/list   (≈/experimental list)
//   ListMCPServerStatus        → mcpServerStatus/list       (≈/mcp)
//   ListApps                   → app/list                   (≈/apps)
//   ListSkills                 → skills/list                (≈/plugins source)
//   ReadAccount                → account/read               (≈/status — account part)
//   ReadRateLimits             → account/rateLimits/read    (≈/status — usage part)
//   GetAuthStatus              → getAuthStatus              (≈/status — auth part)
//
// All wire shapes verified live against codex 0.121.0 (see
// tests/fixtures/v040_probes/). All methods return typed values; the
// Raw escape hatch on Config preserves forward-compat fields the SDK
// has not yet enumerated.
//
// Connection contract: each method requires Connect() to have
// succeeded. Calling on an unconnected or closed client returns an
// error before reaching the wire.
// ====================================================================

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
	resp, err := c.sendRaw(ctx, "ReadConfig", "config/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.ConfigReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadConfig: decode response: %w", err)
	}
	// Populate Raw with every field codex serialized (including ones
	// the curated struct didn't capture).
	var wrapper struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(resp, &wrapper); err == nil {
		result.Config.Raw = wrapper.Config
	}
	return &result.Config, nil
}

// ListModels returns every model the authenticated principal can
// route turns to. Each ModelInfo includes capabilities, supported
// reasoning effort tiers, and any pending upgrade redirects.
//
// Read-only. Maps to the data source for slash command /model.
func (c *Client) ListModels(ctx context.Context) ([]types.ModelInfo, error) {
	resp, err := c.sendRaw(ctx, "ListModels", "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.ModelListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListModels: decode response: %w", err)
	}
	return result.Data, nil
}

// ListExperimentalFeatures returns every feature flag the user can
// toggle via SetExperimentalFeature (Batch 3). Each row carries the
// current Enabled state, DefaultEnabled, and lifecycle Stage.
//
// Read-only. Maps to the data source for slash command /experimental.
func (c *Client) ListExperimentalFeatures(ctx context.Context) ([]types.ExperimentalFeature, error) {
	resp, err := c.sendRaw(ctx, "ListExperimentalFeatures", "experimentalFeature/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.ExperimentalFeatureListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListExperimentalFeatures: decode response: %w", err)
	}
	return result.Data, nil
}

// ListMCPServerStatus returns the runtime state of every configured
// MCP server. The response includes auth status, exposed tools (as a
// map keyed by tool name), resources, and resource templates.
//
// NextCursor is currently always nil in codex 0.121.0 but the SDK
// preserves the pagination affordance for future versions. Callers
// can ignore it for now.
//
// Read-only. Maps to slash command /mcp.
func (c *Client) ListMCPServerStatus(ctx context.Context) (*types.MCPServerStatusListResult, error) {
	resp, err := c.sendRaw(ctx, "ListMCPServerStatus", "mcpServerStatus/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.MCPServerStatusListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListMCPServerStatus: decode response: %w", err)
	}
	return &result, nil
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
	resp, err := c.sendRaw(ctx, "ListApps", "app/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AppListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListApps: decode response: %w", err)
	}
	return result.Data, nil
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
	resp, err := c.sendRaw(ctx, "ListSkills", "skills/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.SkillsListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ListSkills: decode response: %w", err)
	}
	return result.Data, nil
}

// ReadAccount returns the authenticated principal's identity (auth
// type, email when applicable, plan tier).
//
// Read-only. Maps to the account portion of slash command /status.
func (c *Client) ReadAccount(ctx context.Context) (*types.AccountReadResult, error) {
	resp, err := c.sendRaw(ctx, "ReadAccount", "account/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AccountReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadAccount: decode response: %w", err)
	}
	return &result, nil
}

// ReadRateLimits returns a snapshot of the principal's API rate-limit
// usage. Codex returns BOTH a legacy single-bucket view
// (RateLimits) and a multi-bucket view keyed by limit ID
// (RateLimitsByLimitID). Prefer the per-limit map when present.
//
// Read-only. Maps to the usage portion of slash command /status.
func (c *Client) ReadRateLimits(ctx context.Context) (*types.RateLimitsReadResult, error) {
	resp, err := c.sendRaw(ctx, "ReadRateLimits", "account/rateLimits/read", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.RateLimitsReadResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.ReadRateLimits: decode response: %w", err)
	}
	return &result, nil
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
	resp, err := c.sendRaw(ctx, "GetAuthStatus", "getAuthStatus", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result types.AuthStatus
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("codex.Client.GetAuthStatus: decode response: %w", err)
	}
	return &result, nil
}

// ====================================================================
// Internal: generic sendRaw helper used by every read-only method.
// ====================================================================

// sendRaw is the common skeleton for a Send + connection-state +
// RPC-error check. Returns the raw result bytes; caller decodes into
// the typed struct. Embeds the caller name in every error so log
// readers can identify the public method instantly.
//
// Wire-level RPCErrors are translated to typed SDK errors when the
// codex error message matches a known pattern (e.g., "requires
// experimentalApi capability" → *types.FeatureNotEnabledError).
// Otherwise the generic *types.RPCError is returned.
func (c *Client) sendRaw(ctx context.Context, callerName, method string, params any) (json.RawMessage, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("codex.Client.%s: not connected", callerName)
	}
	if c.closed.Load() {
		return nil, fmt.Errorf("codex.Client.%s: client closed", callerName)
	}
	resp, err := c.demux.Send(ctx, method, params)
	if err != nil {
		return nil, fmt.Errorf("codex.Client.%s: %w", callerName, err)
	}
	if resp.Error != nil {
		return nil, classifyRPCError(method, resp.Error)
	}
	return resp.Result, nil
}

// classifyRPCError maps a wire-level RPC error to a typed SDK error
// when the message matches a known pattern. Verified live against
// codex 0.121.0:
//
//	"thread/backgroundTerminals/clean requires experimentalApi capability"
//
// Future patterns can be added here without changing call sites.
func classifyRPCError(method string, e *jsonrpcRPCError) error {
	if e == nil {
		return nil
	}
	if strings.Contains(e.Message, "requires experimentalApi capability") {
		return types.NewFeatureNotEnabledError("experimentalApi", method, e.Message)
	}
	return types.NewRPCError(e.Code, e.Message, e.Data)
}

// jsonrpcRPCError is a local alias for internal/jsonrpc.RPCError —
// avoids importing internal across the public client_commands file
// for one mapping function. Updated when the internal type evolves.
type jsonrpcRPCError = jsonrpc.RPCError

// ====================================================================
// Mutating client methods (Batch 3 of v0.4.0).
//
// These methods change codex state (config.toml, auth state, etc.) or
// send data upstream (feedback). Callers must understand the side
// effects:
//
//   WriteConfigValue / WriteConfigBatch — persist to ~/.codex/config.toml
//   SetExperimentalFeature              — toggles a feature flag in config
//   Logout                              — invalidates ~/.codex/auth.json
//   UploadFeedback                      — sends data to OpenAI servers
//
// Sugar wrappers (SetModel, SetApprovalPolicy, SetSandbox) compose
// WriteConfigValue with the canonical key path for each setting.
// ====================================================================

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
	if keyPath == "" {
		return nil, fmt.Errorf("codex.Client.WriteConfigValue: keyPath must not be empty")
	}
	if strategy == "" {
		strategy = types.MergeReplace
	}
	resp, err := c.sendRaw(ctx, "WriteConfigValue", "config/value/write", types.ConfigEntry{
		KeyPath:       keyPath,
		MergeStrategy: strategy,
		Value:         value,
	})
	if err != nil {
		return nil, err
	}
	var out types.ConfigWriteResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.WriteConfigValue: decode response: %w", err)
	}
	return &out, nil
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
	if len(edits) == 0 {
		return nil, fmt.Errorf("codex.Client.WriteConfigBatch: edits must not be empty")
	}
	// Default mergeStrategy to MergeReplace per-edit when caller left
	// it empty. Mutates a copy so caller's slice is untouched.
	normalized := make([]types.ConfigEntry, len(edits))
	for i, e := range edits {
		if e.MergeStrategy == "" {
			e.MergeStrategy = types.MergeReplace
		}
		normalized[i] = e
	}
	resp, err := c.sendRaw(ctx, "WriteConfigBatch", "config/batchWrite", types.WriteConfigBatchParams{
		Edits: normalized,
	})
	if err != nil {
		return nil, err
	}
	var out types.ConfigWriteResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("codex.Client.WriteConfigBatch: decode response: %w", err)
	}
	return &out, nil
}

// SetModel is sugar for `WriteConfigValue("model", model, MergeReplace)`.
// Mid-session model switching survives subsequent turns.
func (c *Client) SetModel(ctx context.Context, model string) error {
	if model == "" {
		return fmt.Errorf("codex.Client.SetModel: model must not be empty")
	}
	_, err := c.WriteConfigValue(ctx, "model", model, types.MergeReplace)
	return err
}

// SetApprovalPolicy is sugar for
// `WriteConfigValue("approval_policy", policy, MergeReplace)`. Use to
// change codex's approval stance mid-session (e.g., switch from
// `untrusted` to `never` after the user gains confidence).
func (c *Client) SetApprovalPolicy(ctx context.Context, policy types.ApprovalPolicy) error {
	if policy == "" {
		return fmt.Errorf("codex.Client.SetApprovalPolicy: policy must not be empty")
	}
	_, err := c.WriteConfigValue(ctx, "approval_policy", string(policy), types.MergeReplace)
	return err
}

// SetSandbox is sugar for `WriteConfigValue("sandbox", sandbox, MergeReplace)`.
// Changes the sandbox enforcement level for subsequent turns.
func (c *Client) SetSandbox(ctx context.Context, sandbox types.SandboxMode) error {
	if sandbox == "" {
		return fmt.Errorf("codex.Client.SetSandbox: sandbox must not be empty")
	}
	_, err := c.WriteConfigValue(ctx, "sandbox", string(sandbox), types.MergeReplace)
	return err
}

// SetExperimentalFeature toggles one experimental feature flag.
// Sugar around SetExperimentalFeatures for the common single-feature
// case. Equivalent to TUI `/experimental` selecting a feature and
// pressing enter. To list available features, call
// ListExperimentalFeatures.
func (c *Client) SetExperimentalFeature(ctx context.Context, name string, enabled bool) error {
	if name == "" {
		return fmt.Errorf("codex.Client.SetExperimentalFeature: name must not be empty")
	}
	return c.SetExperimentalFeatures(ctx, map[string]bool{name: enabled})
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
	if enablement == nil {
		enablement = map[string]bool{}
	}
	_, err := c.sendRaw(ctx, "SetExperimentalFeatures", "experimentalFeature/enablement/set",
		types.SetExperimentalFeatureParams{Enablement: enablement})
	return err
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
	_, err := c.sendRaw(ctx, "Logout", "account/logout", map[string]any{})
	return err
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
	if report.Classification == "" {
		return nil, fmt.Errorf("codex.Client.UploadFeedback: report.Classification is required")
	}
	if report.IncludeLogs {
		c.logger.Warn("UploadFeedback: IncludeLogs=true — recent thread transcripts will be uploaded to OpenAI",
			zap.String("classification", report.Classification))
	}
	resp, err := c.sendRaw(ctx, "UploadFeedback", "feedback/upload", report)
	if err != nil {
		return nil, err
	}
	var receipt types.FeedbackReceipt
	if err := json.Unmarshal(resp, &receipt); err != nil {
		return nil, fmt.Errorf("codex.Client.UploadFeedback: decode response: %w", err)
	}
	return &receipt, nil
}
