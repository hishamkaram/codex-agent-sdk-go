package codex

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// initTestRepo creates a fresh git repo in t.TempDir() with an
// initial commit so `git diff` has a HEAD to compare against.
// Returns the absolute path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not installed: %v", err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
	} {
		if args[0] == "init" {
			args = append([]string{"-C", dir}, args...)
		}
		cmd := exec.Command("git", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, stderr.String())
		}
	}
	// Initial commit so HEAD exists.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "add", "README.md"},
		{"-C", dir, "commit", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, stderr.String())
		}
	}
	return dir
}

// threadWithCwd builds a Thread stub with just enough state for
// GitDiff's local-only path (no client, no dispatcher).
func threadWithCwd(cwd string) *Thread {
	th := &Thread{id: "test", cwd: cwd}
	th.activeTurnID.Store("")
	return th
}

func TestThread_GitDiff_CleanRepoIsEmpty(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), nil)
	if err != nil {
		t.Fatalf("GitDiff clean: %v", err)
	}
	if result.Stdout != "" {
		t.Errorf("clean repo should have empty diff, got: %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	// Command always starts with `git -C <cwd> diff ...`
	if len(result.Command) < 3 || result.Command[0] != "git" || result.Command[1] != "-C" {
		t.Errorf("command missing -C prefix: %v", result.Command)
	}
}

func TestThread_GitDiff_UnstagedChange(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	// Modify the seeded file without staging.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), nil)
	if err != nil {
		t.Fatalf("GitDiff unstaged: %v", err)
	}
	if !strings.Contains(result.Stdout, "+changed") {
		t.Errorf("diff missing '+changed', got: %q", result.Stdout)
	}
	// `git diff` (without --exit-code) always exits 0, whether or not
	// differences exist.
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestThread_GitDiff_StagedOnly(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	// Stage a modification.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\nstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "README.md")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), &types.GitDiffOptions{Staged: true})
	if err != nil {
		t.Fatalf("GitDiff staged: %v", err)
	}
	if !strings.Contains(result.Stdout, "+staged") {
		t.Errorf("staged diff missing '+staged', got: %q", result.Stdout)
	}
	// Command should include --staged.
	joined := strings.Join(result.Command, " ")
	if !strings.Contains(joined, "--staged") {
		t.Errorf("command missing --staged: %v", result.Command)
	}
}

func TestThread_GitDiff_IncludeAll(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	// Stage a change AND leave another unstaged.
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repo, "add", "a.txt").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\nunstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), &types.GitDiffOptions{IncludeAll: true})
	if err != nil {
		t.Fatalf("GitDiff IncludeAll: %v", err)
	}
	// `git diff HEAD` should show BOTH changes.
	if !strings.Contains(result.Stdout, "+staged") {
		t.Errorf("IncludeAll missing '+staged': %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "+unstaged") {
		t.Errorf("IncludeAll missing '+unstaged': %q", result.Stdout)
	}
	// Command should include HEAD.
	if !strings.Contains(strings.Join(result.Command, " "), "diff HEAD") {
		t.Errorf("command missing 'diff HEAD': %v", result.Command)
	}
}

func TestThread_GitDiff_StatusOnly(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	// Add an untracked file — shows in `git status --porcelain` as `??`.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), &types.GitDiffOptions{StatusOnly: true})
	if err != nil {
		t.Fatalf("GitDiff status: %v", err)
	}
	if !strings.Contains(result.Stdout, "?? new.txt") {
		t.Errorf("status output missing '?? new.txt': %q", result.Stdout)
	}
	// Command should be `git -C <cwd> status --porcelain`.
	if !strings.Contains(strings.Join(result.Command, " "), "status --porcelain") {
		t.Errorf("command wrong: %v", result.Command)
	}
}

func TestThread_GitDiff_MaxBytesTruncation(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	// Create a big change.
	big := strings.Repeat("longline of text is indeed long\n", 200)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repo, "add", "big.txt").Run(); err != nil {
		t.Fatal(err)
	}
	th := threadWithCwd(repo)

	result, err := th.GitDiff(context.Background(), &types.GitDiffOptions{
		Staged:   true,
		MaxBytes: 256,
	})
	if err != nil {
		t.Fatalf("GitDiff MaxBytes: %v", err)
	}
	if !result.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(result.Stdout) > 256 {
		t.Errorf("stdout length %d > MaxBytes 256", len(result.Stdout))
	}
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
	th.markClosed()
	_, err := th.GitDiff(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on closed thread")
	}
	if !strings.Contains(err.Error(), "thread closed") {
		t.Errorf("err = %q", err)
	}
}

func TestCappedWriter_PastLimit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := &cappedWriter{buf: &buf, limit: 5}
	n, err := w.Write([]byte("abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Errorf("returned n = %d, want 10 (full claim)", n)
	}
	if buf.String() != "abcde" {
		t.Errorf("buf = %q, want 'abcde'", buf.String())
	}
	if !w.truncated {
		t.Error("truncated flag not set")
	}
	// Subsequent writes are fully dropped.
	n, _ = w.Write([]byte("xyz"))
	if n != 3 {
		t.Errorf("second write n = %d, want 3", n)
	}
	if buf.String() != "abcde" {
		t.Errorf("buf mutated on second write: %q", buf.String())
	}
}
