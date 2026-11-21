package hookbridge

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdklog "github.com/hishamkaram/codex-agent-sdk-go/internal/log"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func newTestInstallationResources(t *testing.T) *installationResources {
	t.Helper()
	resources := &installationResources{logger: sdklog.NewLoggerFromZap(nil)}
	t.Cleanup(func() {
		if resources.configLease != nil {
			if err := releaseHookConfigLease(resources.configLease); err != nil {
				t.Error(err)
			}
		}
	})
	return resources
}

func TestInstallHooksJSON_NoExistingConfig_RemoveOnRestore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	resources := newTestInstallationResources(t)

	if err := resources.installHooksJSON(home, "/path/to/shim"); err != nil {
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
	if resources.backupPath != "" {
		t.Errorf("hookBackupPath should be empty when no prior config: %q", resources.backupPath)
	}

	resources.restoreHooksJSON()
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

	resources := newTestInstallationResources(t)
	if err := resources.installHooksJSON(home, "/path/to/sdk-shim"); err != nil {
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
	if resources.backupPath == "" {
		t.Fatal("hookBackupPath empty after backup")
	}
	backup, err := os.ReadFile(resources.backupPath)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if !bytes.Equal(backup, original) {
		t.Errorf("backup not byte-identical:\nwant: %s\ngot:  %s", original, backup)
	}

	// Restore — original must come back byte-for-byte and backup file removed.
	resources.restoreHooksJSON()
	restored, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json gone after restore: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Errorf("restored hooks.json not byte-identical to original:\nwant: %s\ngot:  %s", original, restored)
	}
	if _, err := os.Stat(resources.backupPath); !os.IsNotExist(err) {
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

	resources := newTestInstallationResources(t)
	if err := resources.installHooksJSON(home, "/path/to/new-shim"); err != nil {
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
	backup, err := os.ReadFile(resources.backupPath)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if !bytes.Equal(backup, usersOriginal) {
		t.Errorf("install should have backed up the recovered user original, got: %s", backup)
	}

	// Stale backup must be cleaned up.
	if _, statErr := os.Stat(staleBackupPath); !os.IsNotExist(statErr) {
		t.Errorf("stale backup should be removed after recovery: %v", statErr)
	}

	// Round-trip: restore returns the user's original byte-for-byte.
	resources.restoreHooksJSON()
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

	resources := newTestInstallationResources(t)
	err := resources.installHooksJSON(home, "/shim")
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
	if resources.hooksPath != "" {
		t.Errorf("hookHooksJSONPath should be empty on refusal, got %q", resources.hooksPath)
	}
	if resources.backupPath != "" {
		t.Errorf("hookBackupPath should be empty on refusal, got %q", resources.backupPath)
	}
}

func TestInstallationCloseWithoutSetup(t *testing.T) {
	t.Parallel()
	var installation Installation
	installation.Close()
}

func writeTestShim(t *testing.T) string {
	t.Helper()
	shim := filepath.Join(t.TempDir(), "codex-sdk-hook-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return shim
}

func setHookTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func setupTestInstallation(t *testing.T) (*Installation, []string) {
	t.Helper()
	setHookTestHome(t, t.TempDir())
	installation := &Installation{}
	env, err := installation.Setup(t.Context(), InstallationConfig{
		ShimPath: writeTestShim(t),
		Handler:  types.DefaultAllowHookHandler,
	})
	if err != nil {
		t.Fatalf("setup installation: %v", err)
	}
	t.Cleanup(installation.Close)
	return installation, env
}

func TestInstallationDefaultsToIsolatedCodexHome(t *testing.T) {
	installation, env := setupTestInstallation(t)

	userHooks := filepath.Join(os.Getenv("HOME"), ".codex", "hooks.json")
	if _, err := os.Stat(userHooks); !os.IsNotExist(err) {
		t.Fatalf("user hooks.json should not be created in isolated mode, stat err=%v", err)
	}

	codexHome := installation.resources.codexHome
	socket := installation.resources.listener.SocketPath()
	if len(env) != 2 || env[0] != "CODEX_HOME="+codexHome || env[1] != "CODEX_SDK_HOOK_SOCKET="+socket {
		t.Fatalf("app-server env = %q, want isolated home %q and hook socket %q", env, codexHome, socket)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "hooks.json")); err != nil {
		t.Fatalf("isolated hooks.json missing: %v", err)
	}
}

func TestInstallationCloseConcurrentIsRaceFree(t *testing.T) {
	installation, _ := setupTestInstallation(t)
	codexHome := installation.resources.codexHome
	socket := installation.resources.listener.SocketPath()

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(installation.Close)
	}
	workers.Wait()

	if installation.resources != (installationResources{}) {
		t.Fatal("installation retained resources after Close")
	}
	for _, path := range []string{codexHome, socket} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was not removed on Close: %v", path, err)
		}
	}
}

func TestInstallationSetupFailureRemovesRelocatedSocket(t *testing.T) {
	home := filepath.Join(t.TempDir(), strings.Repeat("h", maxUnixSocketLen))
	setHookTestHome(t, home)
	runtimeDir, err := os.MkdirTemp("", "cxrt-")
	if err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(runtimeDir); removeErr != nil {
			t.Errorf("remove runtime directory: %v", removeErr)
		}
	})
	if len(filepath.Join(runtimeDir, "cxh-0000000000", "h.sock")) > maxUnixSocketLen {
		t.Skip("temporary directory too long for a relocated listener")
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	var installation Installation
	env, err := installation.Setup(t.Context(), InstallationConfig{
		ShimPath: writeTestShim(t),
		Handler:  types.DefaultAllowHookHandler,
		Mode:     "invalid",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported hook config mode") {
		t.Fatalf("Setup error = %v, want unsupported hook config mode", err)
	}
	if env != nil {
		t.Errorf("failed installation returned environment additions: %v", env)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed installation left a socket directory: %v, read error: %v", entries, err)
	}
	installation.Close()
}
