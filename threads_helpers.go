package codex

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// maxDefaultGitDiffBytes caps Thread.GitDiff's captured stdout when
// the caller leaves GitDiffOptions.MaxBytes unset.
const maxDefaultGitDiffBytes = 1 << 20 // 1 MiB

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
	if t.closed.Load() {
		return nil, fmt.Errorf("codex.Thread.GitDiff: thread closed")
	}
	if t.cwd == "" {
		return nil, fmt.Errorf("codex.Thread.GitDiff: thread cwd is empty (set ThreadOptions.Cwd at StartThread)")
	}
	args := buildGitArgs(t.cwd, opts)
	maxBytes := maxDefaultGitDiffBytes
	if opts != nil && opts.MaxBytes > 0 {
		maxBytes = opts.MaxBytes
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	// Cap stdout writes at maxBytes+1 so we can detect truncation.
	cappedStdout := &cappedWriter{buf: &stdout, limit: maxBytes}
	cmd.Stdout = cappedStdout
	cmd.Stderr = &stderr
	// Explicitly ensure `git -C` sees our cwd (not the test's cwd).
	// exec.Command already uses os.Getwd by default; -C flag overrides.

	runErr := cmd.Run()

	result := &types.GitDiffResult{
		Command:   append([]string{"git"}, args...),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: cappedStdout.truncated,
		ExitCode:  0,
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			// Exit code 1 from `git diff` means "differences exist" —
			// that's success, not an error. Only return errors for
			// non-0/1 exit codes (or for non-ExitError failures like
			// "git not found" / ctx-canceled).
			if result.ExitCode == 1 {
				return result, nil
			}
			return result, fmt.Errorf("codex.Thread.GitDiff: git exited %d: %s",
				result.ExitCode, bytes.TrimSpace(stderr.Bytes()))
		}
		return result, fmt.Errorf("codex.Thread.GitDiff: %w", runErr)
	}
	return result, nil
}

// buildGitArgs composes the argv vector for Thread.GitDiff based on
// the options. Always leads with `-C <cwd>` so the working directory
// is explicit regardless of the caller's cwd.
func buildGitArgs(cwd string, opts *types.GitDiffOptions) []string {
	args := []string{"-C", cwd}
	switch {
	case opts != nil && opts.IncludeAll:
		args = append(args, "diff", "HEAD")
	case opts != nil && opts.StatusOnly:
		args = append(args, "status", "--porcelain")
	case opts != nil && opts.Staged:
		args = append(args, "diff", "--staged")
	default:
		args = append(args, "diff")
	}
	return args
}

// cappedWriter is a bytes.Buffer wrapper that stops writing once
// `limit` bytes have been accumulated. The truncated flag flips the
// first time a write was partial or fully-dropped.
type cappedWriter struct {
	buf       *bytes.Buffer
	limit     int
	truncated bool
}

// Write implements io.Writer.
func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil // claim acceptance, discard
	}
	if len(p) > remaining {
		w.truncated = true
		_, _ = w.buf.Write(p[:remaining])
		return len(p), nil // drop the excess but report full accept
	}
	return w.buf.Write(p)
}
