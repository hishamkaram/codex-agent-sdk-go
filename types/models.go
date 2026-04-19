package types

import "encoding/json"

// ModelListResult is the response of `model/list`. Codex returns a
// single page (no cursor for this method) of every model the
// authenticated principal can route to.
//
// Wire shape (verified live):
//
//	{"data": [<ModelInfo>, ...]}
type ModelListResult struct {
	Data []ModelInfo `json:"data"`
}

// ModelInfo describes one model the codex app-server is willing to
// route turns to.
type ModelInfo struct {
	// ID is the canonical model identifier (e.g., "gpt-5.4",
	// "gpt-5.2-codex"). Use this when calling SetModel or
	// WriteConfigValue("model", id).
	ID string `json:"id"`
	// Model is a duplicate of ID in codex 0.121.0; preserved for
	// forward-compat in case codex de-duplicates them.
	Model string `json:"model,omitempty"`
	// DisplayName is the user-facing name shown in the TUI's /model
	// picker (often equals ID).
	DisplayName string `json:"displayName,omitempty"`
	// Description is the one-liner shown next to the model name.
	Description string `json:"description,omitempty"`
	// Hidden means the model is not exposed in the default /model
	// picker but can still be selected programmatically.
	Hidden bool `json:"hidden"`
	// IsDefault marks the model the server uses when no override is
	// in effect. Exactly one model has IsDefault=true per response.
	IsDefault bool `json:"isDefault,omitempty"`
	// InputModalities lists the input types the model accepts.
	// Observed: ["text"], ["text", "image"].
	InputModalities []string `json:"inputModalities,omitempty"`
	// SupportedReasoningEfforts lists every reasoning depth the user
	// can pick. Empty for non-reasoning models.
	SupportedReasoningEfforts []ReasoningEffortInfo `json:"supportedReasoningEfforts,omitempty"`
	// DefaultReasoningEffort is one of the SupportedReasoningEfforts
	// values. Empty for non-reasoning models.
	DefaultReasoningEffort string `json:"defaultReasoningEffort,omitempty"`
	// SupportsPersonality is true when codex's `/personality` slash
	// command applies to this model.
	SupportsPersonality bool `json:"supportsPersonality,omitempty"`
	// AdditionalSpeedTiers lists optional speed modes (e.g., "fast"
	// for `/fast` slash command).
	AdditionalSpeedTiers []string `json:"additionalSpeedTiers,omitempty"`
	// Upgrade names the model codex will redirect to when this model
	// is being deprecated. nil if no upgrade is pending.
	Upgrade *string `json:"upgrade,omitempty"`
	// UpgradeInfo carries the migration markdown shown in the TUI's
	// upgrade banner.
	UpgradeInfo *ModelUpgradeInfo `json:"upgradeInfo,omitempty"`
	// AvailabilityNux is reserved for future client-side onboarding
	// flows; opaque JSON in v0.121.0.
	AvailabilityNux json.RawMessage `json:"availabilityNux,omitempty"`
}

// ReasoningEffortInfo describes one reasoning-effort tier on a model.
type ReasoningEffortInfo struct {
	// ReasoningEffort is the canonical value to set via
	// WriteConfigValue("reasoning.effort", ...). Observed: "low",
	// "medium", "high", "xhigh".
	ReasoningEffort string `json:"reasoningEffort"`
	// Description is the user-facing label shown next to the value.
	Description string `json:"description,omitempty"`
}

// ModelUpgradeInfo carries the deprecation/migration message shown to
// users on a model that has a successor.
type ModelUpgradeInfo struct {
	Model             string  `json:"model"`
	UpgradeCopy       *string `json:"upgradeCopy,omitempty"`
	ModelLink         *string `json:"modelLink,omitempty"`
	MigrationMarkdown string  `json:"migrationMarkdown,omitempty"`
}

// ExperimentalFeatureListResult is the response of
// `experimentalFeature/list`. Each row describes one feature flag the
// user can toggle via `experimentalFeature/enablement/set` (the
// programmatic equivalent of TUI `/experimental`).
//
// Wire shape (verified live):
//
//	{"data": [<ExperimentalFeature>, ...]}
type ExperimentalFeatureListResult struct {
	Data []ExperimentalFeature `json:"data"`
}

// ExperimentalFeature is one entry in experimentalFeature/list.
type ExperimentalFeature struct {
	// Name is the canonical feature key. Use this when calling
	// SetExperimentalFeature(name, enabled).
	Name string `json:"name"`
	// Stage is the feature's lifecycle. Observed: "stable",
	// "underDevelopment", "beta".
	Stage string `json:"stage"`
	// DisplayName / Description / Announcement may be null.
	DisplayName  *string `json:"displayName,omitempty"`
	Description  *string `json:"description,omitempty"`
	Announcement *string `json:"announcement,omitempty"`
	// Enabled is the current effective value (after merging
	// session/user/system config).
	Enabled bool `json:"enabled"`
	// DefaultEnabled is what the feature defaults to when the user
	// has not overridden it.
	DefaultEnabled bool `json:"defaultEnabled"`
}

// SetExperimentalFeatureParams is the request shape for
// `experimentalFeature/enablement/set`. Verified against codex
// 0.121.0 schema:
//
//	{"enablement": {"shell_tool": true, "tool_search": false}}
//
// The map key is the canonical feature name; values are the desired
// boolean state. Omitted features are left unchanged.
type SetExperimentalFeatureParams struct {
	Enablement map[string]bool `json:"enablement"`
}
