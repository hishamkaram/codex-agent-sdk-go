package types

// GitDiffOptions configures `Thread.GitDiff` — the local-helper
// implementation of TUI `/diff`. Runs `git -C <thread-cwd> diff` (or
// variants) via os/exec.
//
// The zero value means "unstaged working-tree diff": equivalent to
// `git diff` without --staged.
type GitDiffOptions struct {
	// Staged switches to `git diff --staged` (changes added with
	// `git add`).
	Staged bool
	// IncludeAll combines staged and unstaged: `git diff HEAD`.
	// Mutually exclusive with Staged and StatusOnly; if multiple are
	// set, IncludeAll wins.
	IncludeAll bool
	// StatusOnly switches to `git status --porcelain` (just file
	// states, no diff body). Useful for cheap presence checks.
	StatusOnly bool
	// MaxBytes truncates captured stdout. Zero defaults to 1 MiB.
	// The result's Truncated flag indicates whether truncation
	// happened.
	MaxBytes int
}

// GitDiffResult is the captured output of a Thread.GitDiff invocation.
type GitDiffResult struct {
	// Command is the argv vector that was executed (for debugging).
	Command []string
	// Stdout is the captured diff output (UTF-8 best-effort).
	Stdout string
	// Stderr captures any git warnings.
	Stderr string
	// Truncated is true when MaxBytes capped Stdout.
	Truncated bool
	// ExitCode is the git process exit code. 0 = success;
	// 1 = differences exist (also success for diff commands);
	// other = real error.
	ExitCode int
}

// RemoteDiffParams is the request shape for `gitDiffToRemote` — the
// codex wire method that diffs the thread's cwd against the remote
// tracking branch (a different concept from /diff which shows working
// tree changes).
//
// Verified live (returns "missing field cwd" without):
//
//	{"cwd": "/abs/path"}
type RemoteDiffParams struct {
	Cwd string `json:"cwd"`
}

// RemoteDiffResult is the response of `gitDiffToRemote`. Verified
// live against codex 0.121.0: codex returns the current HEAD sha
// plus the unified diff against its remote tracking branch.
type RemoteDiffResult struct {
	// Sha is the current HEAD commit hash.
	Sha string `json:"sha"`
	// Diff is the unified diff between HEAD and the remote tracking
	// branch. Empty string when there are no changes.
	Diff string `json:"diff"`
}

// InitAgentsMDOptions configures `Client.InitAgentsMD` — the local-
// helper implementation of TUI `/init`. Writes an AGENTS.md scaffold
// under the supplied directory.
type InitAgentsMDOptions struct {
	// Overwrite controls behavior when AGENTS.md already exists.
	// Default false → return *AGENTSMDExistsError.
	Overwrite bool
	// Template overrides the default AGENTS.md content. Empty =
	// SDK's hardcoded scaffold.
	Template string
}
