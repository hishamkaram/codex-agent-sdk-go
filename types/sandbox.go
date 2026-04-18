package types

// SandboxMode controls what the codex subprocess is allowed to do to the
// filesystem and network. Passed as the "sandbox" field to `thread/start`.
type SandboxMode string

const (
	// SandboxReadOnly permits reading files and running commands that do not
	// mutate state. Any mutation triggers an approval prompt.
	SandboxReadOnly SandboxMode = "read-only"

	// SandboxWorkspaceWrite permits reads, edits, and command execution
	// within the thread's cwd. Operations outside the workspace or that
	// hit the network still require approval.
	SandboxWorkspaceWrite SandboxMode = "workspace-write"

	// SandboxDangerFullAccess disables sandboxing. Equivalent to --yolo on
	// the interactive CLI. Use with extreme care — the agent can do
	// anything your process can do.
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

// ApprovalPolicy controls when the codex server asks the client (via
// server-initiated approval requests) before running an action.
//
// Values MUST match the server's accepted set. As of CLI 0.121.0 the
// server rejects anything outside the 5 constants below with a JSON-RPC
// "unknown variant" error on thread/start.
type ApprovalPolicy string

const (
	// ApprovalUntrusted auto-approves known-safe reads; every state-
	// mutating command prompts. Strictest practical policy.
	ApprovalUntrusted ApprovalPolicy = "untrusted"

	// ApprovalOnFailure only prompts after a command fails — the server
	// runs the agent's plan optimistically and escalates on error.
	ApprovalOnFailure ApprovalPolicy = "on-failure"

	// ApprovalOnRequest is the server default. Prompts for destructive or
	// out-of-workspace operations; auto-approves workspace-local reads.
	ApprovalOnRequest ApprovalPolicy = "on-request"

	// ApprovalGranular delegates per-action policy to a ruleset the server
	// ships (see codex config for the full rule language).
	ApprovalGranular ApprovalPolicy = "granular"

	// ApprovalNever runs everything without prompting. Dangerous in
	// combination with SandboxWorkspaceWrite or SandboxDangerFullAccess.
	ApprovalNever ApprovalPolicy = "never"
)
