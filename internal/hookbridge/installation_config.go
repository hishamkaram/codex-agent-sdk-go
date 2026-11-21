package hookbridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// hookBackupSuffix identifies SDK-written backup files of the user's
// hooks.json. Keep the PID suffix for recovery and older SDK versions.
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

// installHooksJSON acquires the lease before inspecting or editing user config.
// The installation owns the lease until Close restores hooks.json, including
// when Setup rolls back a failed write.
func (r *installationResources) installHooksJSON(home, shimPath string) error {
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		return fmt.Errorf("ensure codex dir: %w", err)
	}
	codexDir, err := filepath.EvalSymlinks(codexDir)
	if err != nil {
		return fmt.Errorf("resolve user Codex directory: %w", err)
	}
	r.configLease, err = acquireHookConfigLease(codexDir)
	if err != nil {
		return err
	}
	hooksJSON, err := GenerateHooksJSON(shimPath, hooksJSONTimeoutSeconds)
	if err != nil {
		return fmt.Errorf("generate hooks.json: %w", err)
	}

	hooksPath := filepath.Join(codexDir, "hooks.json")
	backupPath := filepath.Join(codexDir, fmt.Sprintf("hooks.json%s-%d", hookBackupSuffix, os.Getpid()))
	if detectErr := detectConcurrentSDK(codexDir); detectErr != nil {
		return detectErr
	}
	if recoverErr := r.recoverStaleBackups(codexDir, hooksPath); recoverErr != nil {
		return recoverErr
	}

	original, hadOriginal, err := readIfExists(hooksPath)
	if err != nil {
		return fmt.Errorf("read existing hooks.json: %w", err)
	}
	if hadOriginal {
		if writeErr := writeHookBackup(backupPath, original); writeErr != nil {
			return fmt.Errorf("write hooks.json backup: %w", writeErr)
		}
		r.backupPath = backupPath
	}

	r.hooksPath = hooksPath
	if err := os.WriteFile(hooksPath, hooksJSON, 0o600); err != nil {
		return fmt.Errorf("write hooks.json: %w", err)
	}
	if hadOriginal {
		r.logger.Warn("overwrote ~/.codex/hooks.json for SDK lifetime; original backed up and will be restored on Close",
			zap.String("backup", backupPath))
	}
	return nil
}

// writeHookBackup publishes the recovery file only after writing and closing
// the complete copy. Recovery ignores the temporary filename.
func writeHookBackup(path string, data []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".sdk-hook-backup-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeHookBackup(file.Name()))
		}
	}()
	_, writeErr := file.Write(data)
	if writeCloseErr := errors.Join(writeErr, file.Close()); writeCloseErr != nil {
		return writeCloseErr
	}
	return os.Rename(file.Name(), path)
}

func acquireHookConfigLease(codexDir string) (*os.File, error) {
	path := filepath.Join(codexDir, "hooks.json.sdk-lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open user hook config lock: %w", err)
	}
	locked, err := tryLockHookConfig(file)
	if locked {
		return file, nil
	}
	if err != nil {
		err = fmt.Errorf("lock user hook config at %s: %w", path, err)
	} else {
		err = fmt.Errorf("concurrent codex SDK Client detected at %s; close it first or use isolated hook mode", codexDir)
	}
	return nil, errors.Join(err, file.Close())
}

// detectConcurrentSDK also checks for fresh backups left by SDK versions that
// predate the per-home lease.
func detectConcurrentSDK(codexDir string) error {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no codex dir means no concurrent SDK backups to detect
		}
		return fmt.Errorf("detectConcurrentSDK: read %s: %w", codexDir, err)
	}
	prefix := "hooks.json" + hookBackupSuffix
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) >= staleBackupAge {
			continue // stale — recovery handles it after this check
		}
		return fmt.Errorf(
			"concurrent codex SDK Client detected (fresh backup at %s); "+
				"close the other Client first or use isolated hook mode",
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
func (r *installationResources) recoverStaleBackups(codexDir, hooksPath string) error {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		return fmt.Errorf("scan for stale hook backups: %w", err)
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
		return nil
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
		return fmt.Errorf("read stale hook backup %s: %w", oldest.path, err)
	}
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("restore stale hook backup %s: %w", oldest.path, err)
	}
	r.logger.Warn("recovered hooks.json from stale SDK backup (prior SDK run crashed before Close)",
		zap.String("backup", oldest.path),
		zap.Duration("age", now.Sub(oldest.mtime)))
	// Keep the oldest backup available for another recovery if cleanup fails.
	for _, s := range stale {
		if s.path == oldest.path {
			continue
		}
		if err := removeHookBackup(s.path); err != nil {
			return err
		}
	}
	return removeHookBackup(oldest.path)
}

func removeHookBackup(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove hook backup %s: %w", path, err)
	}
	return nil
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
		if err := removeHookBackup(backupPath); err != nil {
			r.logger.Warn("restored hooks.json but could not remove backup", zap.Error(err))
		}
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

// IsHooksConfigKeyPath reports whether a config edit targets the hook settings.
func IsHooksConfigKeyPath(keyPath string) bool {
	return keyPath == "hooks" || strings.HasPrefix(keyPath, "hooks.")
}
