package hookbridge

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const hookConfigLockBytes = 1

func tryLockHookConfig(file *os.File) (bool, error) {
	var offset windows.Overlapped
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, hookConfigLockBytes, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func releaseHookConfigLease(file *os.File) error {
	// Explicit unlock avoids relying on deferred OS cleanup after CloseHandle.
	var offset windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, hookConfigLockBytes, 0, &offset)
	return errors.Join(err, file.Close())
}
