//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package hookbridge

import (
	"errors"
	"os"
)

func tryLockHookConfig(*os.File) (bool, error) {
	return false, errors.New("user-home hook config locking is unavailable on this platform; use isolated hook mode")
}

func releaseHookConfigLease(file *os.File) error {
	return file.Close()
}
