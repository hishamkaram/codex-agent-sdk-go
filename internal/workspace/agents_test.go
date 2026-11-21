package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestInitAgentsMD_RefusesExistingDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(target, []byte("user-authored content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := InitAgentsMD(dir, nil)
	if err == nil {
		t.Fatal("expected AGENTSMDExistsError, got nil")
	}
	if !types.IsAGENTSMDExistsError(err) {
		t.Errorf("expected *AGENTSMDExistsError, got %T: %v", err, err)
	}
	// Verify the original was NOT touched.
	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
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

	err := InitAgentsMD(dir, &types.InitAgentsMDOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.HasPrefix(string(content), "# AGENTS.md") {
		t.Errorf("overwrite did not replace content: %q", string(content))
	}
	if strings.Contains(string(content), "user-authored") {
		t.Error("old content still present after overwrite")
	}
}

func TestInitAgentsMD_CustomTemplate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	custom := "# Custom\n\nMy project.\n"
	err := InitAgentsMD(dir, &types.InitAgentsMDOptions{Template: custom})
	if err != nil {
		t.Fatalf("custom template: %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != custom {
		t.Errorf("content = %q, want %q", string(content), custom)
	}
}

func TestInitAgentsMD_NonExistentDir(t *testing.T) {
	t.Parallel()
	err := InitAgentsMD(filepath.Join(t.TempDir(), "missing"), nil)
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
	err := InitAgentsMD(f, nil)
	if err == nil {
		t.Fatal("expected error for file path")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("err = %q", err)
	}
}

func TestInitAgentsMD_EmptyDirErrors(t *testing.T) {
	t.Parallel()
	err := InitAgentsMD("", nil)
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
	if !strings.Contains(err.Error(), "dir must not be empty") {
		t.Errorf("err = %q", err)
	}
}
