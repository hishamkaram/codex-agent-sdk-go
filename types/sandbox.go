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
type ApprovalPolicy string

const (
	// ApprovalAuto (default for version-controlled folders) auto-approves
	// workspace-local reads/edits/commands. Network or out-of-workspace
	// operations still prompt.
	ApprovalAuto ApprovalPolicy = "auto"

	// ApprovalReadOnly auto-approves reads; every mutation prompts.
	ApprovalReadOnly ApprovalPolicy = "read-only"

	// ApprovalUntrusted auto-approves known-safe reads; every state-mutating
	// command prompts. Strictest practical policy.
	ApprovalUntrusted ApprovalPolicy = "untrusted"

	// ApprovalNever runs everything without prompting. Dangerous in
	// combination with SandboxWorkspaceWrite or SandboxDangerFullAccess.
	ApprovalNever ApprovalPolicy = "never"

	// ApprovalOnRequest prompts only for a specific class of actions
	// enumerated by the server (e.g., destructive operations).
	ApprovalOnRequest ApprovalPolicy = "on-request"
)
