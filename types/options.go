package types

import "go.uber.org/zap"

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
}

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

// WithCwd sets the default working directory for new threads.
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

	// Images are absolute paths to local image files to attach to the turn.
	// Codex streams them to the model as localImage input variants.
	Images []string
}

// ThreadInfo is the metadata record returned by Codex.ListThreads.
type ThreadInfo struct {
	ThreadID     string `json:"thread_id"`
	Summary      string `json:"summary,omitempty"`
	LastModified string `json:"last_modified,omitempty"` // ISO 8601 UTC
	Cwd          string `json:"cwd,omitempty"`
	Model        string `json:"model,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
}

// ForkResult is returned by Codex.ForkThread. The new thread ID points to
// the branched copy; the source thread is unchanged.
type ForkResult struct {
	SourceThreadID string `json:"source_thread_id"`
	NewThreadID    string `json:"new_thread_id"`
}
