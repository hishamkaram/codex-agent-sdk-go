package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestInitAgentsMD_FreshDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := &Client{} // no wire access needed — helper is pure local
	if err := c.InitAgentsMD(context.Background(), dir, nil); err != nil {
		t.Fatalf("InitAgentsMD: %v", err)
	}
	target := filepath.Join(dir, "AGENTS.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.HasPrefix(string(content), "# AGENTS.md") {
		t.Errorf("template missing '# AGENTS.md' header: %q", string(content[:50]))
	}
	if !strings.Contains(string(content), "## Project Overview") {
		t.Error("template missing 'Project Overview' section")
	}
}

func TestGitDiffToRemote_NotConnected(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	_, err := c.GitDiffToRemote(context.Background(), "/some/path")
	if err == nil {
		t.Fatal("expected error on not-connected")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("err = %q", err)
	}
}

func TestGitDiffToRemote_EmptyCwdErrors(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	_, err := c.GitDiffToRemote(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty cwd")
	}
	if !strings.Contains(err.Error(), "cwd must not be empty") {
		t.Errorf("err = %q", err)
	}
}

// threadWithCwd builds a Thread stub with just enough state for
// GitDiff's local-only path (no client, no dispatcher).
func threadWithCwd(cwd string) *Thread {
	th := newThread(nil, "test")
	th.cwd = cwd
	return th
}

func TestThread_GitDiff_NonGitCwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Plain tempdir, not a git repo.
	th := threadWithCwd(dir)

	_, err := th.GitDiff(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error in non-git cwd")
	}
	if !strings.Contains(err.Error(), "git exited") {
		t.Errorf("err = %q, want 'git exited N'", err)
	}
}

func TestThread_GitDiff_EmptyCwdErrors(t *testing.T) {
	t.Parallel()
	th := threadWithCwd("")
	_, err := th.GitDiff(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty cwd")
	}
	if !strings.Contains(err.Error(), "thread cwd is empty") {
		t.Errorf("err = %q", err)
	}
}

func TestThread_GitDiff_ClosedThreadErrors(t *testing.T) {
	t.Parallel()
	th := threadWithCwd("/some/path")
	th.record.Close()
	_, err := th.GitDiff(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on closed thread")
	}
	if !strings.Contains(err.Error(), "thread closed") {
		t.Errorf("err = %q", err)
	}
}
