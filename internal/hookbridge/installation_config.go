package hookbridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// generated hooks.json. MUST exceed InstallationConfig.Timeout — the SDK's
// listener kills the callback first; codex's own timeout is the
// outer bound.
const hooksJSONTimeoutSeconds = 30

func (r *installationResources) installIsolatedHooksJSON(codexHome, shimPath string) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return fmt.Errorf("ensure isolated CODEX_HOME: %w", err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	hooksJSON, err := GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		return fmt.Errorf("generate hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		return fmt.Errorf("write isolated hooks.json: %w", err)
	}
	r.hooksPath = hooksPath
	return nil
}

// installHooksJSON ensures ~/.codex/hooks.json points at the shim. If
// the user already has a hooks.json, it's copied byte-for-byte to a
// PID-suffixed backup that restoreUserHooksJSON uses during cleanup.
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
func (r *installationResources) installHooksJSON(home, shimPath string) error {
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		return fmt.Errorf("ensure codex dir: %w", err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	backupPath := filepath.Join(codexDir, fmt.Sprintf("hooks.json%s-%d", hookBackupSuffix, os.Getpid()))

	r.recoverStaleBackups(codexDir, hooksPath)
	if err := detectConcurrentSDK(codexDir); err != nil {
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
		r.backupPath = backupPath
	}

	hooksJSON, err := GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		// Roll back the backup we just wrote so we don't leave debris.
		if hadOriginal {
			_ = os.Remove(backupPath)
			r.backupPath = ""
		}
		return fmt.Errorf("generate hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		if hadOriginal {
			_ = os.Remove(backupPath)
			r.backupPath = ""
		}
		return fmt.Errorf("write hooks.json: %w", err)
	}

	r.hooksPath = hooksPath
	if hadOriginal {
		r.logger.Warn("overwrote ~/.codex/hooks.json for SDK lifetime; original backed up and will be restored on Close",
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
func detectConcurrentSDK(codexDir string) error {
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
func (r *installationResources) recoverStaleBackups(codexDir, hooksPath string) {
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
		r.logger.Warn("stale hooks.json backup found but unreadable; leaving in place",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		r.logger.Warn("stale hooks.json backup found but restore failed",
			zap.String("path", oldest.path), zap.Error(err))
		return
	}
	r.logger.Warn("recovered hooks.json from stale SDK backup (prior SDK run crashed before Close)",
		zap.String("backup", oldest.path),
		zap.Duration("age", now.Sub(oldest.mtime)))
	for _, s := range stale {
		_ = os.Remove(s.path)
	}
}

// restoreHooksJSON reverses installation. A backup is copied over hooks.json
// byte-for-byte.
// If no backup exists (user had no hooks.json before Connect), the
// SDK-written hooks.json is removed. Best-effort — failures are logged
// but never propagated.
func (r *installationResources) restoreHooksJSON() {
	hooksPath, backupPath := r.hooksPath, r.backupPath
	if hooksPath == "" {
		return
	}
	if backupPath != "" {
		// Read backup, write back over hooks.json. We use read+write
		// (not rename) so a same-mountpoint guarantee isn't required.
		data, err := os.ReadFile(backupPath)
		if err != nil {
			r.logger.Warn("hooks.json backup unreadable; leaving SDK-written config in place",
				zap.String("backup", backupPath), zap.Error(err))
			return
		}
		if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
			r.logger.Warn("hooks.json restore failed; backup retained",
				zap.String("backup", backupPath), zap.Error(err))
			return
		}
		_ = os.Remove(backupPath)
		r.logger.Debug("restored user hooks.json from backup",
			zap.String("hooks_json", hooksPath))
		return
	}
	// No prior config — remove what we wrote.
	if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
		r.logger.Warn("removing SDK-written hooks.json failed",
			zap.String("hooks_json", hooksPath), zap.Error(err))
		return
	}
	r.logger.Debug("removed SDK-written hooks.json (no prior user config)",
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
