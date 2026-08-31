//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package transport

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCLICommandCancellation(cmd *exec.Cmd) {
	// CLI wrappers can fork helpers that inherit stdout/stderr. Isolating the
	// short-lived probe lets context cancellation close the entire descriptor
	// tree instead of waiting for an orphaned helper to exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
