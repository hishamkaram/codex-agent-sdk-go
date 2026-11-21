package hookbridge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestIsolatedInstallationsKeepTheirOwnSockets(t *testing.T) {
	home, err := os.MkdirTemp("", "cx-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	setHookTestHome(t, home)

	shim := writeTestShim(t)
	start := func(reason string) (*Installation, string) {
		t.Helper()
		var installation Installation
		t.Cleanup(installation.Close)
		_, err := installation.Setup(t.Context(), InstallationConfig{
			ShimPath: shim,
			Handler: func(context.Context, types.HookInput) types.HookDecision {
				return types.HookDeny{Reason: reason}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &installation, installation.resources.listener.SocketPath()
	}
	first, firstSocket := start("first installation")
	_, secondSocket := start("second installation")
	if firstSocket == secondSocket {
		t.Fatalf("both installations received socket %q", firstSocket)
	}
	for _, socket := range []string{firstSocket, secondSocket} {
		info, err := os.Stat(filepath.Dir(socket))
		if err != nil {
			t.Fatal(err)
		}
		// Unix enforces the mode bits. Windows uses inherited ACLs.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Fatalf("socket directory mode = %04o, want 0700", info.Mode().Perm())
		}
	}

	check := func(socket, reason string) {
		t.Helper()
		got := dialAndRoundTrip(t, socket, `{"hook_event_name":"preToolUse"}`)
		if got.ExitCode != 2 || got.Stderr != reason {
			t.Fatalf("response = %+v, want denial from %s", got, reason)
		}
	}
	check(firstSocket, "first installation")
	check(secondSocket, "second installation")
	first.Close()
	if _, err := os.Stat(filepath.Dir(firstSocket)); !os.IsNotExist(err) {
		t.Fatalf("first private directory still exists: %v", err)
	}
	check(secondSocket, "second installation")
}

func TestUserHomeInstallationsHoldExclusiveLeaseUntilRestore(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "absent"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setHookTestHome(t, home)
			hooksPath := filepath.Join(home, ".codex", "hooks.json")
			original := []byte("{\"original\":true}\n")
			if existing {
				if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(hooksPath, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := InstallationConfig{
				ShimPath: writeTestShim(t),
				Handler:  types.DefaultAllowHookHandler,
				Mode:     types.HookConfigModeUserHome,
			}
			var first, second Installation
			t.Cleanup(first.Close)
			t.Cleanup(second.Close)
			if _, err := first.Setup(t.Context(), cfg); err != nil {
				t.Fatalf("setup first: %v", err)
			}
			if existing {
				// An old backup still belongs to the live installation.
				old := time.Now().Add(-2 * staleBackupAge)
				if err := os.Chtimes(first.resources.backupPath, old, old); err != nil {
					t.Fatal(err)
				}
			}
			installed, err := os.ReadFile(hooksPath)
			if err != nil {
				t.Fatal(err)
			}
			// Creating symlinks can require administrator privileges on Windows.
			if existing && runtime.GOOS != "windows" {
				aliasHome := t.TempDir()
				if linkErr := os.Symlink(filepath.Dir(hooksPath), filepath.Join(aliasHome, ".codex")); linkErr != nil {
					t.Fatal(linkErr)
				}
				setHookTestHome(t, aliasHome)
			}
			env, err := second.Setup(t.Context(), cfg)
			if env != nil || err == nil || !strings.Contains(err.Error(), "concurrent codex SDK Client detected") {
				t.Fatalf("overlapping setup returned env %v, error %v", env, err)
			}
			second.Close()
			checkHookConfig(t, hooksPath, installed)

			first.Close()
			want := original
			if !existing {
				want = nil
			}
			checkHookConfig(t, hooksPath, want)
			if _, retryErr := second.Setup(t.Context(), cfg); retryErr != nil {
				t.Fatalf("setup after first close: %v", retryErr)
			}
			second.Close()
			checkHookConfig(t, hooksPath, want)
		})
	}
}

func TestUserHomeInstallationReleasesLeaseAfterFailedRecovery(t *testing.T) {
	home := t.TempDir()
	setHookTestHome(t, home)
	codexDir := filepath.Join(home, ".codex")
	hooksPath := filepath.Join(codexDir, "hooks.json")
	// A directory at the config path makes recovery fail on every platform.
	if err := os.MkdirAll(hooksPath, 0o700); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(codexDir, "hooks.json.sdk-backup-12345")
	original := []byte("{\"original\":true}\n")
	if err := os.WriteFile(backupPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleBackupAge)
	if err := os.Chtimes(backupPath, old, old); err != nil {
		t.Fatal(err)
	}

	var installation Installation
	t.Cleanup(installation.Close)
	cfg := InstallationConfig{
		ShimPath: writeTestShim(t),
		Handler:  types.DefaultAllowHookHandler,
		Mode:     types.HookConfigModeUserHome,
	}
	if _, err := installation.Setup(t.Context(), cfg); err == nil ||
		!strings.Contains(err.Error(), "restore stale hook backup") {
		t.Fatalf("setup with blocked recovery = %v, want recovery error", err)
	}
	checkHookConfig(t, backupPath, original)
	if err := os.Remove(hooksPath); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.Setup(t.Context(), cfg); err != nil {
		t.Fatalf("setup after clearing recovery error: %v", err)
	}
	installation.Close()
	checkHookConfig(t, hooksPath, original)
}

func checkHookConfig(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if want == nil {
		if !os.IsNotExist(err) {
			t.Fatalf("hooks config should be absent: %v", err)
		}
		return
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("hooks config = %q, error = %v, want %q", got, err, want)
	}
}

func TestUserHomeInstallationsRejectOtherProcess(t *testing.T) {
	const childEnv = "CODEX_SDK_TEST_HOOK_LEASE_CHILD"
	cfg := InstallationConfig{
		Handler: types.DefaultAllowHookHandler,
		Mode:    types.HookConfigModeUserHome,
	}
	var installation Installation
	t.Cleanup(installation.Close)
	if shim := os.Getenv(childEnv); shim != "" {
		cfg.ShimPath = shim
		_, err := installation.Setup(t.Context(), cfg)
		if err == nil || !strings.Contains(err.Error(), "concurrent codex SDK Client detected") {
			t.Fatalf("child setup returned %v, want exclusive-ownership error", err)
		}
		return
	}

	home := t.TempDir()
	setHookTestHome(t, home)
	cfg.ShimPath = writeTestShim(t)
	// Start with no hooks.json so the holder has no backup for PID-based detection.
	if _, err := installation.Setup(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "hooks.json")
	installed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestUserHomeInstallationsRejectOtherProcess$")
	cmd.Env = append(os.Environ(), childEnv+"="+cfg.ShimPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("contending SDK process failed: %v\n%s", err, out)
	}
	checkHookConfig(t, path, installed)
	installation.Close()
	checkHookConfig(t, path, nil)
}

func TestUserHomeInstallationRollsBackAfterFailedBackupPublication(t *testing.T) {
	home := t.TempDir()
	setHookTestHome(t, home)
	codexDir := filepath.Join(home, ".codex")
	hooksPath := filepath.Join(codexDir, "hooks.json")
	backupDir := filepath.Join(codexDir, fmt.Sprintf("hooks.json.sdk-backup-%d", os.Getpid()))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"original\":true}\n")
	if err := os.WriteFile(hooksPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	var failed, retry Installation
	t.Cleanup(failed.Close)
	t.Cleanup(retry.Close)
	cfg := InstallationConfig{
		ShimPath: writeTestShim(t),
		Handler:  types.DefaultAllowHookHandler,
		Mode:     types.HookConfigModeUserHome,
	}
	env, err := failed.Setup(t.Context(), cfg)
	if env != nil || err == nil || !strings.Contains(err.Error(), "write hooks.json backup") {
		t.Fatalf("setup with blocked backup path = %v, %v, want backup error", env, err)
	}
	checkHookConfig(t, hooksPath, original)
	temps, err := filepath.Glob(filepath.Join(codexDir, ".sdk-hook-backup-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("backup temp files = %v, %v, want none", temps, err)
	}

	if err := os.Remove(backupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := retry.Setup(t.Context(), cfg); err != nil {
		t.Fatalf("setup with another installation after failure: %v", err)
	}
	retry.Close()
	checkHookConfig(t, hooksPath, original)
}
