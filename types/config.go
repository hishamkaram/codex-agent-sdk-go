package types

import "encoding/json"

// ConfigReadResult is the response of `config/read`. The full effective
// codex configuration sits under a "config" wrapper. codex serializes
// every field even when null; callers typically inspect Features and a
// few well-known scalars and ignore the rest.
//
// Wire shape (verified live against codex 0.121.0):
//
//	{"config": {<~80 fields, mostly null defaults>}}
//
// JSON-tag convention NOTE: codex returns this object in **snake_case**
// (e.g., "approval_policy", "default_permissions"). All other v0.4.0
// types use camelCase to match their respective wire shapes — this
// struct is the exception. Each field is mirrored with the canonical
// snake_case codex emits.
type ConfigReadResult struct {
	Config Config `json:"config"`
}

// Config is codex's effective configuration view — the merged result of
// system config, user config, project config, and session overrides.
//
// The shape has ~80 fields with deep nesting. The SDK exposes a
// curated subset (the fields most callers will read or write via
// WriteConfigValue) AND a Raw escape hatch carrying everything else.
//
// Field name convention is **snake_case** to match the on-the-wire
// shape — this is the only struct in the v0.4.0 surface that does so.
type Config struct {
	// Curated subset — the most commonly touched fields. All are
	// pointers to distinguish "unset" from "set to zero value".
	Model              *string         `json:"model,omitempty"`
	ApprovalPolicy     *string         `json:"approval_policy,omitempty"`
	Sandbox            *string         `json:"sandbox,omitempty"`
	DefaultPermissions json.RawMessage `json:"default_permissions,omitempty"`
	Features           map[string]bool `json:"features,omitempty"`
	ApprovalsReviewer  *string         `json:"approvals_reviewer,omitempty"`
	History            json.RawMessage `json:"history,omitempty"`

	// Marketplaces and apps are surfaced because slash commands /apps
	// and /plugins drive off them.
	Marketplaces map[string]json.RawMessage `json:"marketplaces,omitempty"`
	Apps         json.RawMessage            `json:"apps,omitempty"`

	// Raw holds the full payload (every field codex serialized) so
	// callers can read fields the SDK has not curated. Populated by
	// the SDK after unmarshaling — NOT present on the wire.
	Raw map[string]json.RawMessage `json:"-"`
}

// MergeStrategy controls how a config write blends with the
// existing value at the target keyPath. Verified against the codex
// 0.121.0 schema (`MergeStrategy` enum).
type MergeStrategy string

const (
	// MergeReplace overwrites the existing value at keyPath wholesale.
	// Default and recommended for scalar settings (model, sandbox, etc.).
	MergeReplace MergeStrategy = "replace"
	// MergeUpsert merges into the existing value when both are objects.
	// Use when you want to set a nested key without clearing siblings.
	MergeUpsert MergeStrategy = "upsert"
)

// ConfigEntry is one row in a config/batchWrite request, also used as
// the params for config/value/write.
//
// Wire shape (verified against codex 0.121.0 schema):
//
//	{"keyPath": "model", "mergeStrategy": "replace", "value": "gpt-5.4"}
type ConfigEntry struct {
	// KeyPath is a dotted path into config.toml. Top-level examples:
	// "model", "approval_policy", "sandbox". Nested:
	// "features.tool_search", "experimental_network.allowed_domains".
	KeyPath string `json:"keyPath"`
	// MergeStrategy is required by codex; if zero ("") the SDK
	// substitutes MergeReplace before sending.
	MergeStrategy MergeStrategy `json:"mergeStrategy"`
	// Value is JSON-marshaled directly. Strings, bools, numbers, and
	// nested objects are all valid.
	Value any `json:"value"`
}

// WriteConfigBatchParams is the wrapper codex expects for
// config/batchWrite. The param-name "edits" was discovered live —
// codex 0.121.0 errors with `"Invalid request: missing field 'edits'"`
// when this is called with any other wrapper.
type WriteConfigBatchParams struct {
	Edits []ConfigEntry `json:"edits"`
}

// ConfigWriteResponse is the response of config/value/write and
// config/batchWrite. Codex returns metadata about the write so
// clients can verify it landed.
type ConfigWriteResponse struct {
	// Status is the WriteStatus enum. Observed: "ok".
	Status string `json:"status"`
	// Version is a sha256:... hash of the resulting file.
	Version string `json:"version"`
	// FilePath is the absolute path of the file that was written
	// (defaults to ~/.codex/config.toml).
	FilePath string `json:"filePath"`
}
