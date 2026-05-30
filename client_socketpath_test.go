package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRelocateHookSocketUnderPrivateDir verifies the AF_UNIX sun_path overflow
// guard AND the security property that a relocated hook socket lands inside a
// freshly-created 0700 directory — the owner-only access gate for the
// approval-decision channel — never directly in a world-writable temp dir.
func TestRelocateHookSocketUnderPrivateDir(t *testing.T) {
	t.Parallel()

	t.Run("short preferred path used unchanged, no dir created", func(t *testing.T) {
		t.Parallel()
		preferred := "/home/u/.cache/codex-sdk/hook-4242.sock"
		got, dir := relocateHookSocketUnderPrivateDir(preferred, t.TempDir())
		if got != preferred {
			t.Fatalf("socketPath = %q, want preferred %q", got, preferred)
		}
		if dir != "" {
			t.Fatalf("dir = %q, want empty (no relocation, nothing to clean up)", dir)
		}
	})

	t.Run("over-long preferred relocates under a fresh 0700 dir", func(t *testing.T) {
		t.Parallel()
		// preferred overflows unconditionally; base is the real system temp dir
		// (short on normal hosts), so relocation under a fresh 0700 dir is the
		// expected outcome — the same path production takes.
		preferred := "/" + strings.Repeat("x", 120) + "/hook-4242.sock"
		base := os.TempDir()

		got, dir := relocateHookSocketUnderPrivateDir(preferred, base)
		if dir == "" {
			// os.TempDir() is itself pathologically long in this environment, so
			// no relocated socket can fit sun_path; the helper correctly returns
			// preferred rather than leaking a directory. The invariant still holds.
			if got != preferred {
				t.Fatalf("no-relocation path: socketPath = %q, want preferred %q", got, preferred)
			}
			t.Skipf("os.TempDir() %q (len %d) too long to exercise relocation", base, len(base))
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		if len(got) > maxUnixSocketLen {
			t.Fatalf("relocated socket %q len=%d exceeds maxUnixSocketLen=%d", got, len(got), maxUnixSocketLen)
		}
		if filepath.Dir(got) != dir {
			t.Fatalf("relocated socket %q is not directly inside returned dir %q", got, dir)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("relocation dir not created: %v", err)
		}
		// The 0700 parent dir is the atomic owner-only gate that closes the
		// net.Listen→chmod window — assert it explicitly.
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("relocation dir mode = %04o, want 0700 (owner-only gate)", perm)
		}
	})

	t.Run("pathological base returns preferred and leaks no dir", func(t *testing.T) {
		t.Parallel()
		preferred := "/" + strings.Repeat("p", 120) + ".sock"
		// A nonexistent over-long base makes MkdirTemp fail; the helper must
		// return preferred unchanged rather than hide the eventual bind error.
		base := filepath.Join(t.TempDir(), strings.Repeat("q", 200))
		got, dir := relocateHookSocketUnderPrivateDir(preferred, base)
		if got != preferred {
			t.Fatalf("socketPath = %q, want preferred %q", got, preferred)
		}
		if dir != "" {
			t.Fatalf("dir = %q, want empty when MkdirTemp fails", dir)
		}
	})
}

// TestHookSocketFallbackDir verifies the relocated-socket directory prefers a
// usable $XDG_RUNTIME_DIR (per-user, private) and falls back to os.TempDir()
// when it is unset or points at a non-existent path. These subtests mutate the
// environment via t.Setenv, so they cannot run with t.Parallel().
func TestHookSocketFallbackDir(t *testing.T) {
	t.Run("prefers existing XDG_RUNTIME_DIR", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", dir)
		if got := hookSocketFallbackDir(); got != dir {
			t.Fatalf("hookSocketFallbackDir() = %q, want %q", got, dir)
		}
	})

	t.Run("falls back to TempDir when XDG_RUNTIME_DIR is unset", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		if got := hookSocketFallbackDir(); got != os.TempDir() {
			t.Fatalf("hookSocketFallbackDir() = %q, want %q", got, os.TempDir())
		}
	})

	t.Run("falls back to TempDir when XDG_RUNTIME_DIR does not exist", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
		if got := hookSocketFallbackDir(); got != os.TempDir() {
			t.Fatalf("hookSocketFallbackDir() = %q, want %q", got, os.TempDir())
		}
	})
}
