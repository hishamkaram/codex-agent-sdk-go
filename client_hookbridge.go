package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/hookbridge"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
)

// hookBackupSuffix identifies SDK-written backup files of the user's
// hooks.json. The PID suffix lets stale-recovery detect crashed prior
// runs without colliding with a live concurrent SDK instance.
const hookBackupSuffix = ".sdk-backup"

// staleBackupAge is the age threshold past which a leftover backup file
// is treated as evidence of a crashed prior run rather than a live
// concurrent SDK instance.
const staleBackupAge = 60 * time.Second

// hooksJSONTimeoutSeconds is the per-hook timeout written into the
// generated hooks.json. MUST exceed c.opts.HookTimeout — the SDK's
// listener kills the callback first; codex's own timeout is the
// outer bound.
const hooksJSONTimeoutSeconds = 30

// maxUnixSocketLen is a conservative cross-platform budget for an AF_UNIX
// socket path. Linux caps sockaddr_un.sun_path at 108 bytes (107 usable
// before the NUL terminator); macOS/BSD cap it at 104 (103 usable). Using the
// smaller bound guarantees a path chosen here binds on every supported
// platform.
const maxUnixSocketLen = 103

// setupHookBridge starts the Unix socket listener, resolves the shim
// binary, and installs generated hooks.json so codex actually invokes
// the shim. Wires the listener path through CODEX_SDK_HOOK_SOCKET so
// the shim can dial back. Default mode uses an isolated CODEX_HOME;
// user-home mode backs up ~/.codex/hooks.json and restores it on Close.
//
// On any error after the listener starts, this method tears the listener
// down so the caller can return cleanly.
func (c *Client) setupHookBridge(ctx context.Context, extraEnv *[]string) error {
	c.hookBridgeMu.Lock()
	defer c.hookBridgeMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	cacheDir := filepath.Join(home, ".cache", "codex-sdk")
	if mkErr := os.MkdirAll(cacheDir, 0o700); mkErr != nil {
		return fmt.Errorf("cache dir: %w", mkErr)
	}
	// The hook socket is a private IPC rendezvous handed to the shim via
	// CODEX_SDK_HOOK_SOCKET; its location is an implementation detail. The
	// preferred path under $HOME/.cache can overflow the AF_UNIX sun_path
	// limit (~108 bytes) on a deep $HOME or a long CI tempdir, where bind()
	// returns a confusing EINVAL. relocateHookSocketUnderPrivateDir creates a
	// fresh 0700 directory under the fallback base and returns a short socket
	// path inside it so the bind succeeds and the socket stays owner-private.
	preferredSocket := filepath.Join(cacheDir, fmt.Sprintf("hook-%d.sock", os.Getpid()))
	socketPath, socketDir := relocateHookSocketUnderPrivateDir(preferredSocket, hookSocketFallbackDir())
	c.hookSocketDir = socketDir
	// If anything below fails, remove the relocation dir (when we created one).
	// On success it persists and Close removes it alongside the listener.
	hookBridgeReady := false
	defer func() {
		if !hookBridgeReady {
			c.cleanupHookSocketDir()
		}
	}()

	shimPath, err := resolveShimPath(c.opts.ShimPath)
	if err != nil {
		return err
	}

	ln, err := hookbridge.New(ctx, hookbridge.Config{
		SocketPath: socketPath,
		Handler:    c.opts.HookCallback,
		Timeout:    c.opts.HookTimeout,
		Logger:     c.logger,
	})
	if err != nil {
		return err
	}
	c.hookListener = ln

	mode := c.opts.HookConfigMode
	if mode == "" {
		mode = types.HookConfigModeIsolated
	}
	switch mode {
	case types.HookConfigModeIsolated:
		codexHome, err := os.MkdirTemp("", "codex-sdk-home-*")
		if err != nil {
			_ = ln.Close()
			c.hookListener = nil
			return fmt.Errorf("isolated CODEX_HOME: %w", err)
		}
		c.hookCodexHome = codexHome
		if err := c.installIsolatedHooksJSON(codexHome, shimPath); err != nil {
			_ = os.RemoveAll(codexHome)
			c.hookCodexHome = ""
			_ = ln.Close()
			c.hookListener = nil
			return err
		}
		*extraEnv = append(*extraEnv, "CODEX_HOME="+codexHome)
	case types.HookConfigModeUserHome:
		if err := c.installHooksJSON(home, shimPath); err != nil {
			_ = ln.Close()
			c.hookListener = nil
			return err
		}
	default:
		_ = ln.Close()
		c.hookListener = nil
		return fmt.Errorf("unsupported hook config mode %q", mode)
	}

	*extraEnv = append(*extraEnv, "CODEX_SDK_HOOK_SOCKET="+socketPath)
	c.logger.Info("hook bridge ready",
		zap.String("shim", shimPath),
		zap.String("hooks_json", c.hookHooksJSONPath),
		zap.String("socket", socketPath),
		zap.Bool("backed_up_user_config", c.hookHadUserConfig))
	hookBridgeReady = true
	return nil
}

func (c *Client) closeHookBridge() {
	listener, hooksPath, backupPath, codexHome, socketDir := c.takeHookBridgeForClose()
	if listener != nil {
		_ = listener.Close()
	}
	// Restore the user's original ~/.codex/hooks.json (or remove the
	// SDK-written file when no original existed). Logged but never
	// fatal — Close must remain best-effort.
	c.restoreUserHooksJSONPath(hooksPath, backupPath)
	if codexHome != "" {
		if err := os.RemoveAll(codexHome); err != nil {
			c.logger.Warn("removing isolated CODEX_HOME failed",
				zap.String("codex_home", codexHome), zap.Error(err))
		}
	}
	c.removeHookSocketDir(socketDir)
}

func (c *Client) takeHookBridgeForClose() (
	listener *hookbridge.Listener,
	hooksPath string,
	backupPath string,
	codexHome string,
	socketDir string,
) {
	c.hookBridgeMu.Lock()
	defer c.hookBridgeMu.Unlock()
	listener = c.hookListener
	hooksPath = c.hookHooksJSONPath
	backupPath = c.hookBackupPath
	codexHome = c.hookCodexHome
	socketDir = c.hookSocketDir
	c.hookListener = nil
	c.hookHooksJSONPath = ""
	c.hookBackupPath = ""
	c.hookCodexHome = ""
	c.hookSocketDir = ""
	c.hookHadUserConfig = false
	return listener, hooksPath, backupPath, codexHome, socketDir
}

// cleanupHookSocketDir removes the private 0700 directory that hosted a
// relocated hook socket, if one was created. Safe to call when none was (no-op)
// and idempotent. Best-effort: a failure is logged, never fatal.
func (c *Client) cleanupHookSocketDir() {
	if c.hookSocketDir == "" {
		return
	}
	c.removeHookSocketDir(c.hookSocketDir)
	c.hookSocketDir = ""
}

func (c *Client) removeHookSocketDir(socketDir string) {
	if socketDir == "" {
		return
	}
	if err := os.RemoveAll(socketDir); err != nil {
		c.logger.Warn("removing hook socket dir failed",
			zap.String("dir", socketDir), zap.Error(err))
	}
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

func (c *Client) installIsolatedHooksJSON(codexHome, shimPath string) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return fmt.Errorf("ensure isolated CODEX_HOME: %w", err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	hooksJSON, err := hookbridge.GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		return fmt.Errorf("generate hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		return fmt.Errorf("write isolated hooks.json: %w", err)
	}
	c.hookHooksJSONPath = hooksPath
	c.hookHadUserConfig = false
	return nil
}

// installHooksJSON ensures ~/.codex/hooks.json points at the shim. If
// the user already has a hooks.json, it's copied byte-for-byte to a
// PID-suffixed backup that restoreUserHooksJSON consults on Close.
//
// Stale-recovery: if a backup exists from a crashed prior run
// (>staleBackupAge old), this method restores it before installing the
// generated config so the user's data is never lost across crashes.
//
// Concurrent-SDK detection: if a fresh (<staleBackupAge) backup exists
// from a different PID, returns an error rather than silently chaining
// backups (which would corrupt the user's original on Close). v0.3.0
// chose refuse-with-error over last-writer-wins to avoid silent data
// loss; merge-mode is on the v0.3.1 roadmap.
func (c *Client) installHooksJSON(home, shimPath string) error {
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		return fmt.Errorf("ensure codex dir: %w", err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	backupPath := filepath.Join(codexDir, fmt.Sprintf("hooks.json%s-%d", hookBackupSuffix, os.Getpid()))

	c.recoverStaleBackups(codexDir, hooksPath)
	if err := c.detectConcurrentSDK(codexDir); err != nil {
		return err
	}

	original, hadOriginal, err := readIfExists(hooksPath)
	if err != nil {
		return fmt.Errorf("read existing hooks.json: %w", err)
	}
	if hadOriginal {
		if writeErr := os.WriteFile(backupPath, original, 0o600); writeErr != nil {
			return fmt.Errorf("write hooks.json backup: %w", writeErr)
		}
		c.hookBackupPath = backupPath
	}

	hooksJSON, err := hookbridge.GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		// Roll back the backup we just wrote so we don't leave debris.
		if hadOriginal {
			_ = os.Remove(backupPath)
			c.hookBackupPath = ""
		}
		return fmt.Errorf("generate hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		if hadOriginal {
			_ = os.Remove(backupPath)
			c.hookBackupPath = ""
		}
		return fmt.Errorf("write hooks.json: %w", err)
	}

	c.hookHooksJSONPath = hooksPath
	c.hookHadUserConfig = hadOriginal
	if hadOriginal {
		c.logger.Warn("overwrote ~/.codex/hooks.json for SDK lifetime; original backed up and will be restored on Close",
			zap.String("backup", backupPath))
	}
	return nil
}

// detectConcurrentSDK refuses to install hooks.json when a fresh
// (<staleBackupAge) backup file from a different PID exists in
// codexDir. Such a file means another live SDK Client is currently
// managing this hooks.json — chaining a second install would corrupt
// the user's original on Close because each Close restores from its own
// backup, and the LAST Close would write back the previous SDK's
// generated config instead of the user's true original.
func (c *Client) detectConcurrentSDK(codexDir string) error {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no codex dir means no concurrent SDK backups to detect
		}
		return fmt.Errorf("detectConcurrentSDK: read %s: %w", codexDir, err)
	}
	prefix := "hooks.json" + hookBackupSuffix
	myPIDSuffix := fmt.Sprintf("-%d", os.Getpid())
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if strings.HasSuffix(e.Name(), myPIDSuffix) {
			continue // same PID — re-Connect within one process is its own bug; let it fail downstream
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= staleBackupAge {
			continue // stale — recoverStaleBackups already handled
		}
		return fmt.Errorf(
			"concurrent codex SDK Client detected (fresh backup at %s); "+
				"v0.3.0 supports only one HookCallback-enabled Client per machine — "+
				"close the other Client first or run without WithHookCallback",
			filepath.Join(codexDir, e.Name()))
	}
	return nil
}

// recoverStaleBackups looks for SDK backup files older than
// staleBackupAge in codexDir. A backup that old means a prior SDK run
// crashed before Close could restore. Restore the oldest such backup
// over hooks.json (so the user's original survives the crash) and then
// remove all stale backups. Live concurrent SDK runs (whose backups are
// fresher than staleBackupAge) are left alone.
func (c *Client) recoverStaleBackups(codexDir, hooksPath string) {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		return
	}
	prefix := "hooks.json" + hookBackupSuffix
	now := time.Now()
	type candidate struct {
		path  string
		mtime time.Time
	}
	var stale []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		if now.Sub(info.ModTime()) < staleBackupAge {
			continue
		}
		stale = append(stale, candidate{
			path:  filepath.Join(codexDir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(stale) == 0 {
		return
	}
	// Restore the OLDEST stale backup — that's the one most likely to be
	// the user's true original (newer ones may themselves be SDK-written
	// configs that another crashed run backed up).
	oldest := stale[0]
	for _, s := range stale[1:] {
		if s.mtime.Before(oldest.mtime) {
			oldest = s
		}
	}
	data, err := os.ReadFile(oldest.path)
	if err != nil {
		c.logger.Warn("stale hooks.json backup found but unreadable; leaving in place",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		c.logger.Warn("stale hooks.json backup found but restore failed",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	c.logger.Warn("recovered hooks.json from stale SDK backup (prior SDK run crashed before Close)",
		zap.String("backup", oldest.path),
		zap.Duration("age", now.Sub(oldest.mtime)))
	for _, s := range stale {
		_ = os.Remove(s.path)
	}
}

// restoreUserHooksJSON is the Close-time inverse of installHooksJSON.
// If a backup exists, it's renamed back over hooks.json byte-for-byte.
// If no backup exists (user had no hooks.json before Connect), the
// SDK-written hooks.json is removed. Best-effort — failures are logged
// but never propagated.
func (c *Client) restoreUserHooksJSON() {
	c.restoreUserHooksJSONPath(c.hookHooksJSONPath, c.hookBackupPath)
}

func (c *Client) restoreUserHooksJSONPath(hooksPath, backupPath string) {
	if hooksPath == "" {
		return
	}
	if backupPath != "" {
		// Read backup, write back over hooks.json. We use read+write
		// (not rename) so a same-mountpoint guarantee isn't required.
		data, err := os.ReadFile(backupPath)
		if err != nil {
			c.logger.Warn("hooks.json backup unreadable; leaving SDK-written config in place",
				zap.String("backup", backupPath), zap.Error(err))
			return
		}
		if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
			c.logger.Warn("hooks.json restore failed; backup retained",
				zap.String("backup", backupPath), zap.Error(err))
			return
		}
		_ = os.Remove(backupPath)
		c.logger.Debug("restored user hooks.json from backup",
			zap.String("hooks_json", hooksPath))
		return
	}
	// No prior config — remove what we wrote.
	if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
		c.logger.Warn("removing SDK-written hooks.json failed",
			zap.String("hooks_json", hooksPath), zap.Error(err))
		return
	}
	c.logger.Debug("removed SDK-written hooks.json (no prior user config)",
		zap.String("hooks_json", hooksPath))
}

// readIfExists returns the file's contents if present. If the file does
// not exist, returns (nil, false, nil). Other I/O errors are propagated.
func readIfExists(path string) (data []byte, exists bool, err error) {
	data, err = os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
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
