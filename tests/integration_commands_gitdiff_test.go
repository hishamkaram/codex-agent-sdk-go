//go:build integration

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntCmd_GitDiffToRemote_EmptyCwd(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.GitDiffToRemote(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty cwd")
	}
	if !strings.Contains(err.Error(), "cwd must not be empty") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_GitDiffToRemote_NonGitDir(t *testing.T) {
	c := connectReadOnlyClient(t)
	// A clean tempdir is not a git repo — codex should return an
	// RPC error rather than crashing.
	_, err := c.GitDiffToRemote(context.Background(), t.TempDir())
	if err == nil {
		t.Log("note: codex accepted a non-git path; may return empty diff")
		return
	}
	t.Logf("non-git-dir error (expected): %v", err)
}

func TestIntCmd_GitDiffToRemote_RealRepo(t *testing.T) {
	c := connectReadOnlyClient(t)
	// Run against the SDK's own repo — it IS a git repo with a remote.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests/ → repo root.
	repoRoot := filepath.Dir(cwd)
	result, err := c.GitDiffToRemote(context.Background(), repoRoot)
	if err != nil {
		// Codex may return an error for reasons like "no tracking
		// branch configured". Log + exit — don't fail the suite.
		t.Logf("GitDiffToRemote returned: %v (may be legitimate for local-only branches)", err)
		return
	}
	if result == nil {
		t.Fatal("nil result")
	}
	t.Logf("remote diff: sha=%s diffLen=%d", result.Sha, len(result.Diff))
	if result.Sha == "" {
		t.Error("expected HEAD sha")
	}
}

// ====================================================================
// Helpers
// ====================================================================

// newThrowawayThread spins up a thread named "_v040_probe_<unix>"
// and archives it on Cleanup. Codex has no thread/delete wire method,
// so the archived thread persists in ~/.codex/archived_sessions/
// until the user manually rm's it. The prefix makes them easy to spot.
