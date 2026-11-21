package hookbridge

import (
	"errors"
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

// allocateHookSocket gives each installation its own socket name. On Unix,
// the owner-only directory also protects the socket between bind and chmod.
func allocateHookSocket(bases ...string) (socketPath, dir string, err error) {
	var failures []error
	for _, base := range bases {
		if base == "" {
			continue
		}
		dir, err := os.MkdirTemp(base, "cxh-")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		socketPath := filepath.Join(dir, "h.sock")
		if len(socketPath) <= maxUnixSocketLen {
			return socketPath, dir, nil
		}
		if err := os.Remove(dir); err != nil {
			return "", "", fmt.Errorf("remove unusable hook socket directory: %w", err)
		}
		failures = append(failures, fmt.Errorf("socket path under %q exceeds %d bytes", base, maxUnixSocketLen))
	}
	if len(failures) == 0 {
		return "", "", errors.New("no base directory configured for hook socket")
	}
	return "", "", fmt.Errorf("allocate private hook socket directory: %w", errors.Join(failures...))
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
