package codex

import (
	"context"
	"fmt"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/commands"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/workspace"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// InitAgentsMD writes an AGENTS.md scaffold into the given directory.
// Equivalent to TUI `/init`. The scaffold is a hardcoded template
// (override via InitAgentsMDOptions.Template).
//
// Behavior:
//   - If dir does not exist → returns wrapped *os.PathError
//   - If dir/AGENTS.md exists AND opts.Overwrite is false →
//     returns *types.AGENTSMDExistsError
//   - If dir/AGENTS.md exists AND opts.Overwrite is true → overwrite
//   - Otherwise: write template, return nil
//
// The file is written with 0644 permissions.
//
// InitAgentsMD is a LOCAL helper — it does NOT reach the wire. The
// ctx parameter is accepted for consistency but is not consulted;
// file writes are fast enough to not need cancellation.
func (c *Client) InitAgentsMD(ctx context.Context, dir string, opts *types.InitAgentsMDOptions) error {
	return workspace.InitAgentsMD(dir, opts)
}

// GitDiffToRemote returns the diff between the current working
// directory and the remote tracking branch, as computed by codex.
// Distinct from Thread.GitDiff (which runs `git diff` locally against
// the working tree).
//
// Wire method: `gitDiffToRemote` — verified live against codex
// 0.121.0; requires an absolute `cwd` path, errors otherwise.
//
// This is a Client-level method (not Thread-level) because the cwd
// is a parameter, not an implicit thread property.
func (c *Client) GitDiffToRemote(ctx context.Context, cwd string) (*types.RemoteDiffResult, error) {
	return commands.GitDiffToRemote(ctx, c.sendRaw, cwd)
}

// GitDiff runs `git diff` (or a variant) in this thread's cwd and
// returns the captured output. Equivalent to TUI `/diff`.
//
// This is a LOCAL helper — it shells out to the system `git` binary
// via os/exec (no shell, no string concat, args vector only). It does
// NOT use the `gitDiffToRemote` wire method; see
// Client.GitDiffToRemote for the wire-based alternative.
//
// Default (opts nil or zero) runs `git diff` (unstaged working tree).
// Options:
//   - Staged     → `git diff --staged`
//   - IncludeAll → `git diff HEAD` (both staged + unstaged)
//   - StatusOnly → `git status --porcelain` (file states, no diff body)
//   - MaxBytes   → truncate stdout at N bytes; 0 defaults to 1 MiB
//
// When the options conflict, IncludeAll > StatusOnly > Staged.
//
// Errors:
//   - thread.Cwd() is empty → ErrGitDiffNoCwd-style message
//   - cwd is not a git working tree → git exits non-zero; returned
//     via GitDiffResult.ExitCode and err is set to a wrapped ExitError
//   - context canceled → ctx.Err() propagated
func (t *Thread) GitDiff(ctx context.Context, opts *types.GitDiffOptions) (*types.GitDiffResult, error) {
	if t.isClosed() {
		return nil, fmt.Errorf("codex.Thread.GitDiff: %w", types.ErrThreadClosed)
	}
	if t.cwd == "" {
		return nil, fmt.Errorf("codex.Thread.GitDiff: thread cwd is empty (set ThreadOptions.Cwd at StartThread)")
	}
	return workspace.GitDiff(ctx, t.cwd, opts)
}
