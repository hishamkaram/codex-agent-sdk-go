package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// foreignBackup writes a hooks.json SDK backup file attributed to a PID other
// than this test process, aged by `age`, into dir.
func foreignBackup(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	name := "hooks.json" + hookBackupSuffix + fmt.Sprintf("-%d", os.Getpid()+1)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

// TestDetectConcurrentSDK covers detectConcurrentSDK, including the
// os.IsNotExist fast path (a missing ~/.codex means no backups, hence no
// concurrent SDK) which the production install flow reaches whenever the
// directory does not yet exist or is removed between MkdirAll and the scan.
func TestDetectConcurrentSDK(t *testing.T) {
	t.Parallel()

	t.Run("missing dir returns nil", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t)
		if err := c.detectConcurrentSDK(filepath.Join(t.TempDir(), "absent")); err != nil {
			t.Fatalf("missing dir: want nil, got %v", err)
		}
	})

	t.Run("empty dir returns nil", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t)
		if err := c.detectConcurrentSDK(t.TempDir()); err != nil {
			t.Fatalf("empty dir: want nil, got %v", err)
		}
	})

	t.Run("fresh foreign-PID backup is detected", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t)
		dir := t.TempDir()
		foreignBackup(t, dir, 0)
		err := c.detectConcurrentSDK(dir)
		if err == nil {
			t.Fatal("fresh foreign backup: want error, got nil")
		}
		if !strings.Contains(err.Error(), "concurrent codex SDK Client detected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("stale foreign-PID backup is ignored", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t)
		dir := t.TempDir()
		foreignBackup(t, dir, 2*staleBackupAge)
		if err := c.detectConcurrentSDK(dir); err != nil {
			t.Fatalf("stale backup: want nil, got %v", err)
		}
	})
}
