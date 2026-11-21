package types

import "encoding/json"

// MCPServerStatusDetail controls how much data a status page fetches.
type MCPServerStatusDetail string

const (
	MCPServerStatusDetailFull             MCPServerStatusDetail = "full"
	MCPServerStatusDetailToolsAndAuthOnly MCPServerStatusDetail = "toolsAndAuthOnly"
)

// MCPServerStatusListOptions selects one page for Client.ListMCPServerStatusPage.
// EnvironmentID requires both ThreadID and ServerName. Omitting Detail uses
// the provider's full-detail default.
type MCPServerStatusListOptions struct {
	Cursor        *string               `json:"cursor,omitempty"`
	Limit         *uint32               `json:"limit,omitempty"`
	ThreadID      string                `json:"threadId,omitempty"`
	ServerName    string                `json:"serverName,omitempty"`
	EnvironmentID string                `json:"environmentId,omitempty"`
	Detail        MCPServerStatusDetail `json:"detail,omitempty"`
}

// MCPServerStatusListResult is one page of mcpServerStatus/list. A nil
// NextCursor marks the last page. Otherwise, pass the exact cursor and the
// same filters to continue.
type MCPServerStatusListResult struct {
	Data       []MCPServerStatus `json:"data"`
	NextCursor *string           `json:"nextCursor,omitempty"`
}

// MCPServerStatus describes one configured MCP server's runtime state.
// Distinct from McpServerStatusInfo (the config-side type in mcp.go) —
// this one is reported by the server, not requested.
type MCPServerStatus struct {
	// Name is the server identifier from config.toml.
	Name string `json:"name"`
	// EnvironmentID identifies the environment of this registration. Nil
	// denotes a thread-wide registration or an older server that omitted it.
	EnvironmentID *string `json:"environmentId,omitempty"`
	// AuthStatus describes how the SDK is authenticating to the
	// server. Observed: "bearerToken", "oauth", "none".
	AuthStatus string `json:"authStatus,omitempty"`
	// Tools is a MAP from tool-name to tool-metadata, NOT an array.
	// Keys are tool identifiers; values vary by server type.
	Tools map[string]json.RawMessage `json:"tools,omitempty"`
	// Resources is the list of MCP resources the server exposes.
	Resources []json.RawMessage `json:"resources,omitempty"`
	// ResourceTemplates is the list of MCP resource templates.
	ResourceTemplates []json.RawMessage `json:"resourceTemplates,omitempty"`
}

// AppListResult is the response of `app/list`. This is the
// programmatic equivalent of TUI `/apps`.
//
// CAVEAT: in v0.121.0 against ChatGPT auth, this often errors with
// HTTP 403 Forbidden upstream. The error surfaces as a JSON-RPC error
// (RPCError code -32603) with a Cloudflare interstitial in the
// message. The SDK exposes the method but documents that it requires
// elevated workspace permissions.
//
// Wire shape (when successful — UNVERIFIED, structure based on docs):
//
//	{"data": [<AppInfo>, ...]}
type AppListResult struct {
	Data []AppInfo `json:"data"`
}

// AppInfo is one entry in app/list. The exact field set is unverified
// because all probe attempts returned 403; the type is permissive.
type AppInfo struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Source      string          `json:"source,omitempty"` // "local" | "marketplace"
	Version     string          `json:"version,omitempty"`
	Enabled     bool            `json:"enabled,omitempty"`
	Raw         json.RawMessage `json:"-"` // populated post-decode for forward-compat
}

// SkillsListResult is the response of `skills/list`. Codex returns
// skills GROUPED BY discovery directory — each top-level row covers
// one cwd's discovered skills. To get a flat list, iterate
// SkillsCwdGroup.Skills across every Data entry.
//
// Wire shape (verified live):
//
//	{"data": [{"cwd": "...", "errors": [...], "skills": [<Skill>, ...]}, ...]}
type SkillsListResult struct {
	Data []SkillsCwdGroup `json:"data"`
}

// SkillsCwdGroup is one cwd's worth of discovered skills. The errors
// field collects parse failures (e.g., a SKILL.md with malformed
// frontmatter); skills proceed for valid entries.
type SkillsCwdGroup struct {
	Cwd    string            `json:"cwd"`
	Errors []json.RawMessage `json:"errors,omitempty"`
	Skills []Skill           `json:"skills,omitempty"`
}

// Skill is one discovered skill. Mirrors what the TUI's `/plugins`
// (when scope is set) and `/mcp` show.
type Skill struct {
	// Name is the skill identifier (matches the directory name).
	Name string `json:"name"`
	// Path is the absolute path to the SKILL.md or SKILL.json file.
	Path string `json:"path,omitempty"`
	// Scope says where the skill was discovered. Observed values:
	// "system" (codex built-in), "user" ($HOME/.agents/skills),
	// "project" (./.agents/skills).
	Scope string `json:"scope,omitempty"`
	// Enabled is the merged config setting (defaults to true unless
	// the user has disabled the skill via skills/config/write).
	Enabled bool `json:"enabled"`
	// Description is the long-form description used by codex's
	// trigger logic to decide when to load the skill.
	Description string `json:"description,omitempty"`
	// Interface is the user-facing metadata (display name, icons,
	// short description, default prompt).
	Interface *SkillInterface `json:"interface,omitempty"`
	// Dependencies declares MCP servers / tools the skill needs to
	// function. Empty for self-contained skills.
	Dependencies *SkillDependencies `json:"dependencies,omitempty"`
}

// SkillInterface holds the UI-facing metadata for a skill.
type SkillInterface struct {
	DisplayName      string  `json:"displayName,omitempty"`
	ShortDescription string  `json:"shortDescription,omitempty"`
	DefaultPrompt    string  `json:"defaultPrompt,omitempty"`
	BrandColor       *string `json:"brandColor,omitempty"`
	IconLarge        string  `json:"iconLarge,omitempty"`
	IconSmall        string  `json:"iconSmall,omitempty"`
}

// SkillDependencies declares external dependencies. Currently only
// MCP-server tool dependencies are observed in the wild.
type SkillDependencies struct {
	Tools []json.RawMessage `json:"tools,omitempty"`
}
