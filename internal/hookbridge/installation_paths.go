package hookbridge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// maxUnixSocketLen is a conservative cross-platform budget for an AF_UNIX
// socket path. Linux caps sockaddr_un.sun_path at 108 bytes (107 usable
// before the NUL terminator); macOS/BSD cap it at 104 (103 usable). Using the
// smaller bound guarantees a path chosen here binds on every supported
// platform.
const maxUnixSocketLen = 103

func compatibleUserHookHome(expected, requested string) bool {
	if requested == "" || requested == expected {
		return true
	}
	if !filepath.IsAbs(requested) {
		return false
	}
	expectedInfo, expectedErr := os.Stat(expected)
	requestedInfo, requestedErr := os.Stat(requested)
	return expectedErr == nil && requestedErr == nil && os.SameFile(expectedInfo, requestedInfo)
}

// relocateHookSocketUnderPrivateDir handles the case where the preferred hook
// socket path overflows the AF_UNIX sun_path budget. It creates a fresh 0700
// directory under base (os.MkdirTemp always uses mode 0700 and a collision-free
// random name) and returns a short socket path inside it, plus that directory so
// the caller can remove it on Close.
//
// The 0700 parent is the authoritative access gate and is what makes the
// relocation safe: it closes the window between net.Listen (which creates the
// socket at the process umask — commonly group-connectable) and the listener's
// chmod, during which a socket placed directly in a world-writable $TMPDIR would
// be connectable by other local users. That is unacceptable for a channel that
// carries tool-approval (allow/deny) decisions. The preferred path already lives
// under the 0700 ~/.cache/codex-sdk directory; this gives the fallback the same
// guarantee atomically at creation rather than relying on the post-bind chmod.
//
// When preferred already fits, it is returned unchanged with an empty dir (no
// relocation, nothing to clean up). If even the relocated path would overflow
// sun_path, the temp dir is removed and preferred is returned unchanged so bind
// surfaces the platform error as before rather than leaking a directory.
func relocateHookSocketUnderPrivateDir(preferred, base string) (socketPath, dir string) {
	if len(preferred) <= maxUnixSocketLen {
		return preferred, ""
	}
	d, err := os.MkdirTemp(base, "cxh-")
	if err != nil {
		return preferred, ""
	}
	relocated := filepath.Join(d, "h.sock")
	if len(relocated) > maxUnixSocketLen {
		_ = os.RemoveAll(d)
		return preferred, ""
	}
	return relocated, d
}

// hookSocketFallbackDir returns the base directory under which a relocated hook
// socket's private 0700 directory is created when the preferred path under
// $HOME/.cache would overflow the AF_UNIX sun_path limit. It prefers
// $XDG_RUNTIME_DIR — the per-user, 0700, short-path runtime directory provided
// for exactly this purpose on Linux — and falls back to os.TempDir(). On macOS
// os.TempDir() is already a per-user private directory; on Linux it is the
// world-writable /tmp, which is why the relocated socket is never placed there
// directly but inside a freshly-created 0700 subdirectory (see
// relocateHookSocketUnderPrivateDir). A set-but-stale XDG_RUNTIME_DIR (not an
// existing directory) is ignored so bind never fails on a dead path.
func hookSocketFallbackDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return os.TempDir()
}

// resolveShimPath finds the codex-sdk-hook-shim binary. Order:
//  1. explicit ShimPath option
//  2. exec.LookPath (PATH)
//  3. $GOPATH/bin, $HOME/go/bin, ./.bin
func resolveShimPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("shim at %q: %w", explicit, err)
		}
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	if p, err := exec.LookPath("codex-sdk-hook-shim"); err == nil {
		return p, nil
	}
	// Fall-back search locations.
	var roots []string
	if gp := os.Getenv("GOPATH"); gp != "" {
		roots = append(roots, filepath.Join(gp, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "go", "bin"))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(cwd, ".bin"))
	}
	for _, root := range roots {
		candidate := filepath.Join(root, "codex-sdk-hook-shim")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("codex-sdk-hook-shim not found on PATH, $GOPATH/bin, $HOME/go/bin, or ./.bin (install: go install github.com/hishamkaram/codex-agent-sdk-go/cmd/codex-sdk-hook-shim@latest)")
}
