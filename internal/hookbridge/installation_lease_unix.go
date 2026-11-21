//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package hookbridge

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockHookConfig(file *os.File) (bool, error) {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EWOULDBLOCK):
			return false, nil
		case !errors.Is(err, unix.EINTR):
			return false, err
		}
	}
}

func releaseHookConfigLease(file *os.File) error {
	return file.Close()
}
