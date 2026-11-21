package transport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveAppServerWorkingDirectory captures the physical launch cwd once.
// Codex resolves relative thread paths against the subprocess's native cwd,
// which is the symlink target when the child was launched through an alias.
func resolveAppServerWorkingDirectory(cwd string) (string, error) {
	if filepath.IsAbs(cwd) {
		return filepath.EvalSymlinks(cwd)
	}
	if runtime.GOOS == "windows" {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	parent, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Anchor with the native spelling so resolving a parent component
	// walks any intervening symlink before moving up a directory.
	return filepath.EvalSymlinks(strings.TrimSuffix(parent, "/") + "/" + cwd)
}
