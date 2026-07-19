package types

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"
)

// CodexOptions configures the Codex client. Construct with NewCodexOptions
// and chain With* methods.
//
// The With* methods mutate the receiver and return it (they do NOT clone).
// This matches Go's builder convention and avoids allocation during
// chain-building.
type CodexOptions struct {
	// --- Transport / subprocess knobs ---

	// CLIPath overrides CLI discovery. If empty, the SDK searches PATH +
	// ~/.codex/bin + brew + npm install paths.
	CLIPath string

	// ExtraArgs are appended to the `codex app-server` argv. Rarely needed.
	ExtraArgs []string

	// Env is a list of "KEY=VALUE" entries overlaid on os.Environ for the
	// subprocess. A "KEY=" entry (empty value) unsets KEY. OPENAI_API_KEY
	// passes through from os.Environ by default.
	Env []string

	// Logger is the injected zap logger. If nil, a no-op logger is used.
	Logger *zap.Logger

	// ReadBufferSize overrides the demux read-buffer ceiling. 0 uses the
	// 2 MiB default (MinReadBufferSize). Sizes below the minimum are
	// raised.
	ReadBufferSize int

	// Verbose enables Info+Debug logging when Logger is nil.
	Verbose bool

	// --- Client identity (sent in `initialize` clientInfo) ---

	ClientName    string
	ClientVersion string
	ClientTitle   string

	// --- Defaults applied to new threads (StartThread/ResumeThread) ---
	// Individual threads can override each of these via ThreadOptions.

	DefaultModel          string
	DefaultCwd            string
	DefaultSandbox        SandboxMode
	DefaultApprovalPolicy ApprovalPolicy
	DefaultMCPServers     map[string]McpServerConfig

	// --- Approval handling ---

	// ApprovalCallback is invoked when the server sends a server-initiated
	// approval request. If nil, DefaultDenyApprovalCallback is used — all
	// approval prompts are denied.
	ApprovalCallback ApprovalCallback

	// --- Hook bridge (v0.3.0) ---

	// HookCallback is invoked by the SDK when codex fires a hook handler.
	// Setting it causes Connect to generate hooks.json in an isolated
	// CODEX_HOME by default so codex routes hooks through the SDK shim.
	// User-home mutation is available only via HookConfigModeUserHome. Nil
	// means no bridge is set up — the hooks feature alone only
	// delivers HookStarted/HookCompleted observer events. See docs/hooks.md.
	HookCallback HookHandler

	// HooksEnabled records a WithHooks request. Connect resolves the feature
	// name against the installed CLI because older peers use codex_hooks while
	// current peers use hooks. Never serialized.
	HooksEnabled bool `json:"-"`

	// ShimPath overrides auto-discovery of the codex-sdk-hook-shim binary.
	// When empty, the SDK searches PATH, $GOPATH/bin, $HOME/go/bin, and
	// the project's .bin/ directory. Set this when the shim lives in a
	// non-standard location.
	ShimPath string

	// HookTimeout bounds how long the SDK waits for HookCallback to
	// return before defaulting to HookAllow{}. 0 uses the 30s default.
	// MUST be shorter than the timeout baked into the generated
	// hooks.json — otherwise codex kills the shim before the SDK
	// responds.
	HookTimeout time.Duration

	// HookConfigMode controls where WithHookCallback writes generated
	// hooks.json. Empty means HookConfigModeIsolated.
	HookConfigMode HookConfigMode

	// --- Capability negotiation (v0.4.0) ---

	// ExperimentalAPI opts the connection into experimental codex
	// methods that require the `experimentalApi` capability flag in
	// the initialize handshake. Defaults to false to preserve v0.3.x
	// behavior.
	//
	// Methods that require this flag (verified live against codex
	// 0.144.1):
	//   - thread/backgroundTerminals/clean
	//
	// Calling such methods without this option set returns a
	// *types.FeatureNotEnabledError.
	ExperimentalAPI bool

	// OptOutNotificationMethods asks codex app-server not to emit exact
	// notification methods listed here. This is a transport-volume knob only;
	// callers must not rely on it for correctness.
	OptOutNotificationMethods []string

	// --- Observability + reliability ---

	// Observer receives SDK lifecycle and health telemetry (connect, first
	// message, subprocess exit, decode errors, give-up, backpressure, unknown
	// frames). A nil Observer drops all telemetry (NopObserver semantics) and is
	// fully backwards-compatible. Never serialized.
	Observer Observer `json:"-"`

	// MaxConsecutiveParseErrors caps how many consecutive inbound JSON-RPC decode
	// failures the demux tolerates before it authoritatively terminates the
	// subprocess (rather than looping on garbage forever and leaking a zombie).
	// nil or 0 uses the demux default. Pointer so the zero value is
	// distinguishable from "explicitly set to default". Never serialized.
	MaxConsecutiveParseErrors *uint `json:"-"`
}

type HookConfigMode string

const (
	// HookConfigModeIsolated writes hooks.json into a temporary CODEX_HOME
	// owned by the SDK Client. This is the default and never mutates user
	// ~/.codex.
	HookConfigModeIsolated HookConfigMode = "isolated"
	// HookConfigModeUserHome writes ~/.codex/hooks.json for the Client
	// lifetime, backing up and restoring any user config on Close.
	HookConfigModeUserHome HookConfigMode = "user_home"
)

// NewCodexOptions returns a CodexOptions populated with sensible defaults:
// sandbox read-only, approval policy on-request (the server default), no
// CLI path override, no MCP servers.
func NewCodexOptions() *CodexOptions {
	return &CodexOptions{
		ClientName:            "codex-agent-sdk-go",
		ClientVersion:         "0.1.0",
		DefaultSandbox:        SandboxReadOnly,
		DefaultApprovalPolicy: ApprovalOnRequest,
	}
}

// WithCLIPath sets an explicit path to the codex binary.
func (o *CodexOptions) WithCLIPath(path string) *CodexOptions { o.CLIPath = path; return o }

// WithExtraArgs appends to `codex app-server` argv.
func (o *CodexOptions) WithExtraArgs(args ...string) *CodexOptions {
	o.ExtraArgs = append(o.ExtraArgs, args...)
	return o
}

// WithEnv overlays KEY=VALUE entries onto os.Environ for the subprocess.
func (o *CodexOptions) WithEnv(entries ...string) *CodexOptions {
	o.Env = append(o.Env, entries...)
	return o
}

// WithLogger injects a pre-configured zap logger. Passing nil silences
// the SDK.
func (o *CodexOptions) WithLogger(l *zap.Logger) *CodexOptions { o.Logger = l; return o }

// WithReadBufferSize overrides the demux read-buffer ceiling. Below-minimum
// sizes are silently raised to 2 MiB.
func (o *CodexOptions) WithReadBufferSize(n int) *CodexOptions {
	o.ReadBufferSize = n
	return o
}

// WithVerbose toggles Info+Debug logging for the internal logger. No-op if
// a custom Logger is provided via WithLogger.
func (o *CodexOptions) WithVerbose(v bool) *CodexOptions { o.Verbose = v; return o }

// WithClientInfo sets the name/version/title reported in the `initialize`
// handshake.
func (o *CodexOptions) WithClientInfo(name, version, title string) *CodexOptions {
	o.ClientName = name
	o.ClientVersion = version
	o.ClientTitle = title
	return o
}

// WithModel sets the default model for new threads.
func (o *CodexOptions) WithModel(m string) *CodexOptions { o.DefaultModel = m; return o }

// WithCwd sets the app-server's initial working directory and the default
// working directory for new threads. Configure it before Connect when the
// subprocess must launch outside the caller's working directory.
func (o *CodexOptions) WithCwd(cwd string) *CodexOptions { o.DefaultCwd = cwd; return o }

// WithSandbox sets the default sandbox mode for new threads.
func (o *CodexOptions) WithSandbox(s SandboxMode) *CodexOptions { o.DefaultSandbox = s; return o }

// WithApprovalPolicy sets the default approval policy for new threads.
func (o *CodexOptions) WithApprovalPolicy(p ApprovalPolicy) *CodexOptions {
	o.DefaultApprovalPolicy = p
	return o
}

// WithMCPServers sets the default MCP server configuration for new threads.
// Replaces any previously-set map.
func (o *CodexOptions) WithMCPServers(servers map[string]McpServerConfig) *CodexOptions {
	o.DefaultMCPServers = servers
	return o
}

// WithApprovalCallback registers the approval handler. See
// ApprovalCallback doc for lifetime/panic rules.
func (o *CodexOptions) WithApprovalCallback(cb ApprovalCallback) *CodexOptions {
	o.ApprovalCallback = cb
	return o
}

// WithFeatureEnabled appends one or more `--enable <name>` flags to the
// `codex app-server` argv. Feature flags gate experimental codex
// subsystems; see `codex features list` for the current set.
//
// Repeat calls accumulate — WithFeatureEnabled("a").WithFeatureEnabled("b")
// enables both.
func (o *CodexOptions) WithFeatureEnabled(names ...string) *CodexOptions {
	for _, n := range names {
		if n == "" {
			continue
		}
		o.ExtraArgs = append(o.ExtraArgs, "--enable", n)
	}
	return o
}

// WithHooks is the convenience shortcut for enabling the hooks
// feature flag. When true, codex emits hook/started and hook/completed
// notifications for registered hook handlers (see
// ~/.codex/hooks.json or a CODEX_HOME override). The SDK observes them
// as *types.HookStarted and *types.HookCompleted events.
//
// When false or unset, hook wire methods are never emitted.
func (o *CodexOptions) WithHooks(enabled bool) *CodexOptions {
	if enabled {
		o.HooksEnabled = true
	}
	return o
}

// WithHookCallback registers a Go function that codex invokes for every
// hook event (preToolUse, postToolUse, sessionStart, userPromptSubmit,
// stop). The SDK manages the full bridge lifecycle: spawns a Unix socket
// listener, generates a hooks.json pointing at the codex-sdk-hook-shim
// binary, sets CODEX_HOME to a tempdir, and passes
// CODEX_SDK_HOOK_SOCKET to the codex subprocess.
//
// This option implies WithHooks(true). Because the SDK generates and owns the
// exact ephemeral command hook definition, it also bypasses Codex's interactive
// hook-trust prompt for this invocation.
//
// WARNING: the callback runs inside the SDK process on the bridge
// listener's goroutine. Do NOT call Thread.Run, Thread.RunStreamed, or
// any other SDK operation that would deadlock the dispatcher.
func (o *CodexOptions) WithHookCallback(h HookHandler) *CodexOptions {
	o.HookCallback = h
	return o.WithHooks(true)
}

// WithHookConfigMode sets where WithHookCallback installs generated hooks
// config. The default is HookConfigModeIsolated.
func (o *CodexOptions) WithHookConfigMode(mode HookConfigMode) *CodexOptions {
	o.HookConfigMode = mode
	return o
}

// WithShimPath overrides shim-binary auto-discovery. Set this when
// codex-sdk-hook-shim lives outside PATH / $GOPATH/bin / .bin/.
func (o *CodexOptions) WithShimPath(path string) *CodexOptions {
	o.ShimPath = path
	return o
}

// WithHookTimeout sets the per-callback timeout. If the callback doesn't
// return within this duration, the SDK defaults to HookAllow{} and logs
// a warning. Must be shorter than the hooks.json subprocess timeout
// (30s default).
func (o *CodexOptions) WithHookTimeout(d time.Duration) *CodexOptions {
	o.HookTimeout = d
	return o
}

// WithExperimentalAPI opts the connection into experimental codex
// methods. See CodexOptions.ExperimentalAPI for the current list.
func (o *CodexOptions) WithExperimentalAPI(enabled bool) *CodexOptions {
	o.ExperimentalAPI = enabled
	return o
}

// WithOptOutNotificationMethods sets exact app-server notification methods to
// suppress during initialize capability negotiation.
func (o *CodexOptions) WithOptOutNotificationMethods(methods ...string) *CodexOptions {
	o.OptOutNotificationMethods = append([]string(nil), methods...)
	return o
}

// WithObserver sets the telemetry Observer for SDK lifecycle and health events.
// Passing nil restores NopObserver semantics (telemetry dropped).
func (o *CodexOptions) WithObserver(obs Observer) *CodexOptions {
	o.Observer = obs
	return o
}

// ObserverOrNop returns the configured Observer, or NopObserver{} when none is
// set (or the receiver is nil). This is the single nil-guard owner: every
// emission site calls ObserverOrNop so individual sites never repeat the nil
// check.
func (o *CodexOptions) ObserverOrNop() Observer {
	if o == nil || o.Observer == nil {
		return NopObserver{}
	}
	return o.Observer
}

// WithMaxConsecutiveParseErrors sets how many consecutive inbound decode
// failures the demux tolerates before terminating the subprocess as
// unrecoverable. A value of 0 is ignored (the demux default applies).
func (o *CodexOptions) WithMaxConsecutiveParseErrors(n uint) *CodexOptions {
	o.MaxConsecutiveParseErrors = &n
	return o
}

// ThreadOptions overrides per-thread defaults when calling StartThread or
// ResumeThread. Zero-value fields fall back to the CodexOptions defaults.
type ThreadOptions struct {
	Model          string
	Cwd            string
	Sandbox        SandboxMode
	ApprovalPolicy ApprovalPolicy
	MCPServers     map[string]McpServerConfig
}

// ResumeOptions is used when resuming a persisted thread. Cwd may be
// overridden on resume (verified supported by the app-server spike).
type ResumeOptions struct {
	Cwd string
}

// RunOptions overrides per-turn knobs when calling Thread.Run or
// Thread.RunStreamed.
type RunOptions struct {
	// OutputSchema constrains the model's final response to match a JSON
	// Schema. Nil means unconstrained text output.
	OutputSchema *OutputSchema

	// CollaborationMode asks Codex app-server to run the turn in an
	// explicit collaboration mode, such as plan mode.
	CollaborationMode *CollaborationMode

	// Skills are explicitly invoked Codex skills to attach to the turn.
	// Codex app-server expects both the prompt text and the structured
	// skill item so it can load the skill immediately instead of relying on
	// trigger inference from the raw text alone.
	Skills []SkillInput

	// Images are absolute paths to local image files to attach to the turn.
	// Codex streams them to the model as localImage input variants.
	Images []string
}

// CollaborationModeKind is the Codex app-server collaboration mode name.
type CollaborationModeKind string

const (
	// CollaborationModeDefault is Codex's normal execution mode.
	CollaborationModeDefault CollaborationModeKind = "default"
	// CollaborationModePlan asks Codex to ask clarifying questions and stop
	// for plan approval before implementation.
	CollaborationModePlan CollaborationModeKind = "plan"
)

// CollaborationMode is serialized into turn/start params as
// collaborationMode.
type CollaborationMode struct {
	Mode     CollaborationModeKind     `json:"mode"`
	Settings CollaborationModeSettings `json:"settings"`
}

// CollaborationModeSettings mirrors Codex app-server's current
// collaboration-mode settings payload.
type CollaborationModeSettings struct {
	Model                 string  `json:"model"`
	DeveloperInstructions *string `json:"developer_instructions,omitempty"`
	ReasoningEffort       *string `json:"reasoning_effort,omitempty"`
}

// SkillInput identifies one discovered skill to attach to a turn.
// Name is the skill identifier and Path is the absolute SKILL.md/SKILL.json
// path returned by Client.ListSkills.
type SkillInput struct {
	Name string
	Path string
}

// ThreadInfo is the metadata record returned by Codex.ListThreads.
type ThreadInfo struct {
	ThreadID     string          `json:"thread_id"`
	Summary      string          `json:"summary,omitempty"`
	LastModified string          `json:"last_modified,omitempty"` // ISO 8601 UTC
	Cwd          string          `json:"cwd,omitempty"`
	Model        string          `json:"model,omitempty"`
	Archived     bool            `json:"archived,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

// ForkResult is returned by Codex.ForkThread. The new thread ID points to
// the branched copy; the source thread is unchanged.
type ForkResult struct {
	SourceThreadID string `json:"source_thread_id"`
	NewThreadID    string `json:"new_thread_id"`
}

// ThreadListOptions configures Client.ListThreadsPage.
type ThreadListOptions struct {
	Limit          int
	Cursor         string
	Archived       *bool
	Cwd            []string
	SearchTerm     string
	SortKey        string
	SortDirection  string
	ModelProviders []string
	SourceKinds    []string
	UseStateDBOnly bool
}

// ThreadListPage is one page of thread/list results.
type ThreadListPage struct {
	Threads         []ThreadInfo
	NextCursor      string
	BackwardsCursor string
	Raw             json.RawMessage
}

// ThreadReadOptions configures Client.ReadThread.
type ThreadReadOptions struct {
	IncludeTurns bool
}

// ThreadReadResult is the parsed result of thread/read.
type ThreadReadResult struct {
	Thread ThreadRecord
	Raw    json.RawMessage
}

// ThreadRecord is persisted Codex thread metadata plus optional turns.
type ThreadRecord struct {
	ID        string
	SessionID string
	Name      string
	Preview   string
	Cwd       string
	Model     string
	Path      string
	Status    string
	CreatedAt string
	UpdatedAt string
	Turns     []ThreadHistoryTurn
	Raw       json.RawMessage
}

// ThreadHistoryTurn is one persisted turn returned by thread/read.
type ThreadHistoryTurn struct {
	ID          string
	Status      string
	StartedAt   string
	CompletedAt string
	Items       []ThreadHistoryItem
	Raw         json.RawMessage
}

// ThreadHistoryItem is a raw persisted item from a thread turn.
type ThreadHistoryItem struct {
	Type string
	ID   string
	Raw  json.RawMessage
}

// GetThreadMessagesOptions configures Client.GetThreadMessages.
type GetThreadMessagesOptions struct {
	Limit  int
	Offset int
}

// ThreadMessage is a read-only persisted user/assistant message extracted
// from thread/read turns.
type ThreadMessage struct {
	Role     string
	ID       string
	ThreadID string
	TurnID   string
	Text     string
	Raw      json.RawMessage
}
