package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChooseHookSocketPath verifies the AF_UNIX sun_path overflow guard:
// short preferred paths pass through unchanged, over-long ones relocate to a
// short PID-keyed name under the fallback dir, and a pathologically long
// fallback dir leaves the (still-too-long) preferred path untouched so bind
// surfaces the platform error rather than this helper hiding it.
func TestChooseHookSocketPath(t *testing.T) {
	t.Parallel()

	const pid = 4242
	tests := []struct {
		name        string
		preferred   string
		fallbackDir string
		wantPref    bool // result must equal preferred verbatim
	}{
		{
			name:        "short preferred path used unchanged",
			preferred:   "/home/u/.cache/codex-sdk/hook-4242.sock",
			fallbackDir: "/tmp",
			wantPref:    true,
		},
		{
			name:        "over-long preferred relocates under fallback dir",
			preferred:   "/tmp/claude-1000/" + strings.Repeat("x", 80) + "/.cache/codex-sdk/hook-4242.sock",
			fallbackDir: "/tmp",
			wantPref:    false,
		},
		{
			name:        "pathological fallback dir returns preferred",
			preferred:   "/" + strings.Repeat("p", 120) + ".sock",
			fallbackDir: "/" + strings.Repeat("q", 120),
			wantPref:    true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Guard the test data itself: the "relocate" case is only
			// meaningful if the preferred path genuinely overflows the limit.
			if !tt.wantPref && len(tt.preferred) <= maxUnixSocketLen {
				t.Fatalf("test setup: preferred %q (len %d) does not exceed maxUnixSocketLen %d",
					tt.preferred, len(tt.preferred), maxUnixSocketLen)
			}

			got := chooseHookSocketPath(tt.preferred, tt.fallbackDir, pid)

			if tt.wantPref {
				if got != tt.preferred {
					t.Fatalf("chooseHookSocketPath = %q, want preferred %q", got, tt.preferred)
				}
				return
			}

			if len(got) > maxUnixSocketLen {
				t.Fatalf("relocated path %q len=%d exceeds maxUnixSocketLen=%d", got, len(got), maxUnixSocketLen)
			}
			want := filepath.Join(tt.fallbackDir, fmt.Sprintf("cxh-%d.sock", pid))
			if got != want {
				t.Fatalf("relocated path = %q, want %q", got, want)
			}
		})
	}
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
