//go:build !windows

package transport

import (
	"io"
	"os"
	"os/exec"
)

func openProcessStdin(cmd *exec.Cmd) (io.WriteCloser, *os.File, error) {
	stdin, err := cmd.StdinPipe()
	return stdin, nil, err
}
