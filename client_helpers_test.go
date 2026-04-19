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

func TestInitAgentsMD_RefusesExistingDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, []byte("user-authored content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Client{}
	err := c.InitAgentsMD(context.Background(), dir, nil)
	if err == nil {
		t.Fatal("expected AGENTSMDExistsError, got nil")
	}
	if !types.IsAGENTSMDExistsError(err) {
		t.Errorf("expected *AGENTSMDExistsError, got %T: %v", err, err)
	}
	// Verify the original was NOT touched.
	content, _ := os.ReadFile(target)
	if string(content) != "user-authored content\n" {
		t.Errorf("refusal did not preserve original: %q", string(content))
	}
}

func TestInitAgentsMD_OverwriteTrue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, []byte("user-authored content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Client{}
	err := c.InitAgentsMD(context.Background(), dir, &types.InitAgentsMDOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	content, _ := os.ReadFile(target)
	if !strings.HasPrefix(string(content), "# AGENTS.md") {
		t.Errorf("overwrite did not replace content: %q", string(content[:50]))
	}
	if strings.Contains(string(content), "user-authored") {
		t.Error("old content still present after overwrite")
	}
}

func TestInitAgentsMD_CustomTemplate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := &Client{}
	custom := "# Custom\n\nMy project.\n"
	err := c.InitAgentsMD(context.Background(), dir, &types.InitAgentsMDOptions{Template: custom})
	if err != nil {
		t.Fatalf("custom template: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if string(content) != custom {
		t.Errorf("content = %q, want %q", string(content), custom)
	}
}

func TestInitAgentsMD_NonExistentDir(t *testing.T) {
	t.Parallel()
	c := &Client{}
	err := c.InitAgentsMD(context.Background(), "/nonexistent/xxx-v040", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
	if !strings.Contains(err.Error(), "stat dir") {
		t.Errorf("err = %q, want 'stat dir'", err)
	}
}

func TestInitAgentsMD_NotADirectory(t *testing.T) {
	t.Parallel()
	// Pass a file path, not a directory.
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	err := c.InitAgentsMD(context.Background(), f, nil)
	if err == nil {
		t.Fatal("expected error for file path")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("err = %q", err)
	}
}

func TestInitAgentsMD_EmptyDirErrors(t *testing.T) {
	t.Parallel()
	c := &Client{}
	err := c.InitAgentsMD(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
	if !strings.Contains(err.Error(), "dir must not be empty") {
		t.Errorf("err = %q", err)
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
