package hookbridge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAllocateHookSocket(t *testing.T) {
	t.Parallel()
	base, err := os.MkdirTemp("", "cx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	t.Run("uses a private directory under the first usable base", func(t *testing.T) {
		t.Parallel()
		socket, dir, err := allocateHookSocket(base, os.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(dir) != base || filepath.Dir(socket) != dir || len(socket) > maxUnixSocketLen {
			t.Fatalf("socket %q, dir %q must fit under base %q", socket, dir, base)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		// Windows uses inherited ACLs for directory access.
		if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o700 {
			t.Fatalf("socket directory mode = %04o, want 0700", perm)
		}
	})

	t.Run("falls back and removes the overlong directory", func(t *testing.T) {
		t.Parallel()
		long := filepath.Join(t.TempDir(), strings.Repeat("x", 110))
		if err := os.Mkdir(long, 0o700); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(base, "missing")
		socket, dir, err := allocateHookSocket(long, missing, "", base)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(dir) != base || filepath.Dir(socket) != dir || len(socket) > maxUnixSocketLen {
			t.Fatalf("socket %q, dir %q must fit under fallback base %q", socket, dir, base)
		}
		entries, err := os.ReadDir(long)
		if err != nil || len(entries) != 0 {
			t.Fatalf("overlong base contains %v, error = %v", entries, err)
		}
	})

	t.Run("fails when every base is unusable", func(t *testing.T) {
		t.Parallel()
		socket, dir, err := allocateHookSocket(filepath.Join(base, "missing"))
		if err == nil || socket != "" || dir != "" {
			t.Fatalf("socket = %q, dir = %q, err = %v, want allocation error", socket, dir, err)
		}
	})
}
