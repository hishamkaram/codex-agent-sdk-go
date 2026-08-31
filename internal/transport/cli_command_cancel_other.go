//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package transport

import "os/exec"

// CommandContext retains its direct-process cancellation on platforms where
// the standard library has no portable process-group primitive.
func configureCLICommandCancellation(_ *exec.Cmd) {}
