package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
)

// newTestClient builds a Client with just enough wiring for the hook
// backup/restore unit tests (no transport, no opts.HookCallback). The
// returned client is safe to call installHooksJSON / restoreUserHooksJSON
// on; nothing else is exercised.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{
		logger: sdklog.NewLoggerFromZap(nil),
	}
}

func TestInstallHooksJSON_NoExistingConfig_RemoveOnRestore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	c := newTestClient(t)

	if err := c.installHooksJSON(home, "/path/to/shim"); err != nil {
		t.Fatalf("installHooksJSON: %v", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json should exist after install: %v", err)
	}
	if !strings.Contains(string(data), `"PreToolUse"`) {
		t.Errorf("generated hooks.json missing PreToolUse: %s", data)
	}
	if !strings.Contains(string(data), `"/path/to/shim"`) {
		t.Errorf("generated hooks.json missing shim path: %s", data)
	}
	if c.hookHadUserConfig {
		t.Error("hookHadUserConfig should be false when no prior config existed")
	}
	if c.hookBackupPath != "" {
		t.Errorf("hookBackupPath should be empty when no prior config: %q", c.hookBackupPath)
	}

	c.restoreUserHooksJSON()
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("hooks.json should be removed after restore (no prior config); stat err = %v", err)
	}
}

func TestInstallHooksJSON_ExistingConfig_BackupAndRestoreByteIdentical(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	original := []byte(`{"hooks":{"PreToolUse":[{"matcher":".*","hooks":[{"type":"command","command":"/usr/local/bin/my-old-shim","timeout":10}]}]}}`)
	if err := os.WriteFile(hooksPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	c := newTestClient(t)
	if err := c.installHooksJSON(home, "/path/to/sdk-shim"); err != nil {
		t.Fatalf("installHooksJSON: %v", err)
	}

	// Generated config must point at sdk-shim now.
	got, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"/path/to/sdk-shim"`) {
		t.Errorf("hooks.json not overwritten with SDK config: %s", got)
	}
	if !c.hookHadUserConfig {
		t.Error("hookHadUserConfig should be true")
	}
	if c.hookBackupPath == "" {
		t.Fatal("hookBackupPath empty after backup")
	}
	backup, err := os.ReadFile(c.hookBackupPath)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if !bytes.Equal(backup, original) {
		t.Errorf("backup not byte-identical:\nwant: %s\ngot:  %s", original, backup)
	}

	// Restore — original must come back byte-for-byte and backup file removed.
	c.restoreUserHooksJSON()
	restored, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json gone after restore: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Errorf("restored hooks.json not byte-identical to original:\nwant: %s\ngot:  %s", original, restored)
	}
	if _, err := os.Stat(c.hookBackupPath); !os.IsNotExist(err) {
		t.Errorf("backup file should be removed after successful restore: %v", err)
	}
}

func TestInstallHooksJSON_StaleBackupRecovered(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Simulate a crashed prior SDK run: an SDK-written hooks.json + a
	// stale backup of the user's true original. Backup mtime is set to
	// 5 minutes ago to clear the staleBackupAge threshold (60s).
	hooksPath := filepath.Join(codexDir, "hooks.json")
	staleSDKConfig := []byte(`{"hooks":{"PreToolUse":[{"matcher":".*","hooks":[{"type":"command","command":"/old/sdk-shim","timeout":30}]}]}}`)
	if err := os.WriteFile(hooksPath, staleSDKConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	usersOriginal := []byte(`{"hooks":{"UserPromptSubmit":[{"matcher":".*","hooks":[{"type":"command","command":"/users/own/handler"}]}]}}`)
	staleBackupPath := filepath.Join(codexDir, "hooks.json.sdk-backup-99999")
	if err := os.WriteFile(staleBackupPath, usersOriginal, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(staleBackupPath, old, old); err != nil {
		t.Fatal(err)
	}

	c := newTestClient(t)
	if err := c.installHooksJSON(home, "/path/to/new-shim"); err != nil {
		t.Fatalf("installHooksJSON: %v", err)
	}

	// After install the on-disk hooks.json is the new SDK-generated config.
	got, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"/path/to/new-shim"`) {
		t.Errorf("expected new SDK config to be installed, got: %s", got)
	}

	// The backup created during install should hold the user's TRUE
	// original (recovered from the stale backup), not the dead
	// SDK-written config that was on disk pre-install.
	backup, err := os.ReadFile(c.hookBackupPath)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if !bytes.Equal(backup, usersOriginal) {
		t.Errorf("install should have backed up the recovered user original, got: %s", backup)
	}

	// Stale backup must be cleaned up.
	if _, err := os.Stat(staleBackupPath); !os.IsNotExist(err) {
		t.Errorf("stale backup should be removed after recovery: %v", err)
	}

	// Round-trip: restore returns the user's original byte-for-byte.
	c.restoreUserHooksJSON()
	restored, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, usersOriginal) {
		t.Errorf("post-restore content not user's original:\nwant: %s\ngot:  %s", usersOriginal, restored)
	}
}

func TestInstallHooksJSON_FreshConcurrentBackupRefused(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A concurrent live SDK has a fresh backup (just-now mtime). v0.3.0
	// refuses to install rather than chaining backups (which would
	// corrupt the user's true original on Close).
	freshBackup := filepath.Join(codexDir, "hooks.json.sdk-backup-12345")
	if err := os.WriteFile(freshBackup, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := newTestClient(t)
	err := c.installHooksJSON(home, "/shim")
	if err == nil {
		t.Fatal("expected concurrent-SDK refusal error, got nil")
	}
	if !strings.Contains(err.Error(), "concurrent codex SDK Client detected") {
		t.Errorf("error doesn't name the concurrent-SDK case: %v", err)
	}
	// Fresh backup must remain in place (we did not touch the other
	// SDK's state).
	if _, err := os.Stat(freshBackup); err != nil {
		t.Errorf("fresh concurrent backup must not be removed: %v", err)
	}
	// Our own state must be unchanged — we never wrote anything.
	if c.hookHooksJSONPath != "" {
		t.Errorf("hookHooksJSONPath should be empty on refusal, got %q", c.hookHooksJSONPath)
	}
	if c.hookBackupPath != "" {
		t.Errorf("hookBackupPath should be empty on refusal, got %q", c.hookBackupPath)
	}
}

func TestRestoreUserHooksJSON_NoOpWhenNeverInstalled(t *testing.T) {
	t.Parallel()
	c := newTestClient(t)
	// Did not call installHooksJSON. restoreUserHooksJSON must be a no-op.
	c.restoreUserHooksJSON()
}
