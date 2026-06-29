package types

import "encoding/json"

// --- v0.2.0 expansion: MCP events ---

// MCPServerStartupStatusUpdated is emitted as each MCP server moves through
// its startup lifecycle (starting -> connected | error).
// Wire method: "mcpServer/startupStatus/updated".
type MCPServerStartupStatusUpdated struct {
	Name   string          `json:"name"`
	Status json.RawMessage `json:"status"`
	Error  *string         `json:"error,omitempty"`
}

func (*MCPServerStartupStatusUpdated) isThreadEvent() {}
func (*MCPServerStartupStatusUpdated) EventMethod() string {
	return "mcpServer/startupStatus/updated"
}

// MCPServerOAuthLoginCompleted is emitted when an MCP server's OAuth flow
// finishes. Wire method: "mcpServer/oauthLogin/completed".
type MCPServerOAuthLoginCompleted struct {
	Name    string  `json:"name"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

func (*MCPServerOAuthLoginCompleted) isThreadEvent() {}
func (*MCPServerOAuthLoginCompleted) EventMethod() string {
	return "mcpServer/oauthLogin/completed"
}

// --- v0.2.0 expansion: account + model events ---

// AccountLoginCompleted is emitted when a login flow finishes.
// Wire method: "account/login/completed".
type AccountLoginCompleted struct {
	Success bool    `json:"success"`
	LoginID *string `json:"login_id,omitempty"`
	Error   *string `json:"error,omitempty"`
}

func (*AccountLoginCompleted) isThreadEvent()      {}
func (*AccountLoginCompleted) EventMethod() string { return "account/login/completed" }

// AccountRateLimitsUpdated is emitted when the server pushes a fresh rate-
// limit snapshot. RateLimits is the raw snapshot payload.
// Wire method: "account/rateLimits/updated".
type AccountRateLimitsUpdated struct {
	RateLimits json.RawMessage `json:"rate_limits"`
}

func (*AccountRateLimitsUpdated) isThreadEvent()      {}
func (*AccountRateLimitsUpdated) EventMethod() string { return "account/rateLimits/updated" }

// AccountUpdated is emitted when the authenticated account's metadata
// changes (plan type, auth mode).
// Wire method: "account/updated".
type AccountUpdated struct {
	AuthMode json.RawMessage `json:"auth_mode,omitempty"`
	PlanType json.RawMessage `json:"plan_type,omitempty"`
}

func (*AccountUpdated) isThreadEvent()      {}
func (*AccountUpdated) EventMethod() string { return "account/updated" }

// ModelRerouted is emitted when the server reroutes a turn to a different
// model (e.g., fast-mode fallback, rate-limit rerouting).
// Wire method: "model/rerouted".
type ModelRerouted struct {
	ThreadID  string          `json:"thread_id"`
	TurnID    string          `json:"turn_id"`
	FromModel string          `json:"from_model"`
	ToModel   string          `json:"to_model"`
	Reason    json.RawMessage `json:"reason"`
}

func (*ModelRerouted) isThreadEvent()      {}
func (*ModelRerouted) EventMethod() string { return "model/rerouted" }

// ModelVerification is emitted when the model reports verification state for a
// turn. Verifications is raw because the upstream enum/payload is intentionally
// loose and may grow.
// Wire method: "model/verification".
type ModelVerification struct {
	ThreadID      string          `json:"thread_id"`
	TurnID        string          `json:"turn_id"`
	Verifications json.RawMessage `json:"verifications"`
}

func (*ModelVerification) isThreadEvent()      {}
func (*ModelVerification) EventMethod() string { return "model/verification" }

// --- v0.2.0 expansion: system / filesystem events ---

// ConfigWarning is emitted when codex detects a suspect config value.
// Wire method: "configWarning".
type ConfigWarning struct {
	Summary string          `json:"summary"`
	Details *string         `json:"details,omitempty"`
	Path    *string         `json:"path,omitempty"`
	Range   json.RawMessage `json:"range,omitempty"`
}

func (*ConfigWarning) isThreadEvent()      {}
func (*ConfigWarning) EventMethod() string { return "configWarning" }

// DeprecationNotice carries a runtime deprecation message from the server.
// Wire method: "deprecationNotice".
type DeprecationNotice struct {
	Summary string  `json:"summary"`
	Details *string `json:"details,omitempty"`
}

func (*DeprecationNotice) isThreadEvent()      {}
func (*DeprecationNotice) EventMethod() string { return "deprecationNotice" }

// Warning carries a server warning. ThreadID is optional on the wire; when
// present, the SDK routes it to that thread.
// Wire method: "warning".
type Warning struct {
	ThreadID *string `json:"thread_id,omitempty"`
	Message  string  `json:"message"`
}

func (*Warning) isThreadEvent()      {}
func (*Warning) EventMethod() string { return "warning" }

// GuardianWarning carries a thread-scoped safety warning from the guardian.
// Wire method: "guardianWarning".
type GuardianWarning struct {
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
}

func (*GuardianWarning) isThreadEvent()      {}
func (*GuardianWarning) EventMethod() string { return "guardianWarning" }

// FsChanged is emitted when codex's filesystem watcher detects changes.
// Wire method: "fs/changed".
type FsChanged struct {
	WatchID      string   `json:"watch_id"`
	ChangedPaths []string `json:"changed_paths"`
}

func (*FsChanged) isThreadEvent()      {}
func (*FsChanged) EventMethod() string { return "fs/changed" }

// SkillsChanged is emitted when the server's skill registry changes.
// Wire method: "skills/changed". Params are not currently exposed (empty
// required set on the schema).
type SkillsChanged struct{}

func (*SkillsChanged) isThreadEvent()      {}
func (*SkillsChanged) EventMethod() string { return "skills/changed" }

// AppListUpdated is emitted when the server's list of available apps
// changes. Wire method: "app/list/updated".
type AppListUpdated struct {
	Data json.RawMessage `json:"data"`
}

func (*AppListUpdated) isThreadEvent()      {}
func (*AppListUpdated) EventMethod() string { return "app/list/updated" }

// ServerRequestResolved is emitted when a previously-pending server-
// initiated request (e.g., an approval) is resolved.
// Wire method: "serverRequest/resolved".
type ServerRequestResolved struct {
	ThreadID  string          `json:"thread_id"`
	RequestID json.RawMessage `json:"request_id"`
}

func (*ServerRequestResolved) isThreadEvent()      {}
func (*ServerRequestResolved) EventMethod() string { return "serverRequest/resolved" }

// ProcessOutputDelta streams base64-encoded output for a process/spawn handle.
// Wire method: "process/outputDelta".
type ProcessOutputDelta struct {
	ProcessHandle string `json:"process_handle"`
	Stream        string `json:"stream"`
	DeltaBase64   string `json:"delta_base64"`
	CapReached    bool   `json:"cap_reached"`
}

func (*ProcessOutputDelta) isThreadEvent()      {}
func (*ProcessOutputDelta) EventMethod() string { return "process/outputDelta" }

// ProcessExited is the terminal process/spawn notification.
// Wire method: "process/exited".
type ProcessExited struct {
	ProcessHandle    string `json:"process_handle"`
	ExitCode         int    `json:"exit_code"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	StdoutCapReached bool   `json:"stdout_cap_reached"`
	StderrCapReached bool   `json:"stderr_cap_reached"`
}

func (*ProcessExited) isThreadEvent()      {}
func (*ProcessExited) EventMethod() string { return "process/exited" }

// CommandExecOutputDelta streams base64-encoded output for a command/exec
// handle. Wire method: "command/exec/outputDelta".
type CommandExecOutputDelta struct {
	ProcessID   string `json:"process_id"`
	Stream      string `json:"stream"`
	DeltaBase64 string `json:"delta_base64"`
	CapReached  bool   `json:"cap_reached"`
}

func (*CommandExecOutputDelta) isThreadEvent()      {}
func (*CommandExecOutputDelta) EventMethod() string { return "command/exec/outputDelta" }

// RemoteControlStatusChanged reports app-server remote-control connectivity.
// Wire method: "remoteControl/status/changed".
type RemoteControlStatusChanged struct {
	EnvironmentID *string `json:"environment_id,omitempty"`
	Status        string  `json:"status"`
}

func (*RemoteControlStatusChanged) isThreadEvent() {}
func (*RemoteControlStatusChanged) EventMethod() string {
	return "remoteControl/status/changed"
}

// ExternalAgentConfigImportCompleted reports completion of an external agent
// config import. Params is preserved raw because 0.130.0 declares an empty
// object and future versions may add fields.
// Wire method: "externalAgentConfig/import/completed".
type ExternalAgentConfigImportCompleted struct {
	Params json.RawMessage `json:"params,omitempty"`
}

func (*ExternalAgentConfigImportCompleted) isThreadEvent() {}
func (*ExternalAgentConfigImportCompleted) EventMethod() string {
	return "externalAgentConfig/import/completed"
}

// --- v0.2.0 expansion: Windows platform events ---

// WindowsWorldWritableWarning is emitted when codex detects world-writable
// paths in the workspace on Windows. Wire method:
// "windows/worldWritableWarning".
type WindowsWorldWritableWarning struct {
	ExtraCount  int      `json:"extra_count"`
	FailedScan  bool     `json:"failed_scan"`
	SamplePaths []string `json:"sample_paths"`
}

func (*WindowsWorldWritableWarning) isThreadEvent() {}
func (*WindowsWorldWritableWarning) EventMethod() string {
	return "windows/worldWritableWarning"
}

// WindowsSandboxSetupCompleted is emitted when Windows sandbox
// initialization finishes. Wire method: "windowsSandbox/setupCompleted".
type WindowsSandboxSetupCompleted struct {
	Success bool            `json:"success"`
	Mode    json.RawMessage `json:"mode"`
	Error   *string         `json:"error,omitempty"`
}

func (*WindowsSandboxSetupCompleted) isThreadEvent() {}
func (*WindowsSandboxSetupCompleted) EventMethod() string {
	return "windowsSandbox/setupCompleted"
}

// --- v0.2.0 expansion: fuzzy file search events ---

// FuzzyFileSearchSessionUpdated is emitted during a fuzzy file search
// session. Wire method: "fuzzyFileSearch/sessionUpdated".
type FuzzyFileSearchSessionUpdated struct {
	Params json.RawMessage `json:"params,omitempty"`
}

func (*FuzzyFileSearchSessionUpdated) isThreadEvent() {}
func (*FuzzyFileSearchSessionUpdated) EventMethod() string {
	return "fuzzyFileSearch/sessionUpdated"
}

// FuzzyFileSearchSessionCompleted is emitted when a fuzzy file search
// session completes. Wire method: "fuzzyFileSearch/sessionCompleted".
type FuzzyFileSearchSessionCompleted struct {
	Params json.RawMessage `json:"params,omitempty"`
}

func (*FuzzyFileSearchSessionCompleted) isThreadEvent() {}
func (*FuzzyFileSearchSessionCompleted) EventMethod() string {
	return "fuzzyFileSearch/sessionCompleted"
}
