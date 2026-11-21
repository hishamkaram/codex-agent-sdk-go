package transport

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const processStdinBufferSize = 16 * 1024
const processStdinBackpressureDelay = 2 * time.Millisecond

// The parent owns a nonblocking byte writer. The child inherits a blocking
// reader. Each native write reports the bytes accepted before it returns, so
// cancellation preserves the pipe when the pending frame sent zero bytes.
func openProcessStdin(cmd *exec.Cmd) (io.WriteCloser, *os.File, error) {
	pipeName := `\\.\pipe\codex-sdk-stdin-` + rand.Text()
	name, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return nil, nil, fmt.Errorf("encode stdin pipe name: %w", err)
	}
	security, err := currentUserPipeSecurity()
	if err != nil {
		return nil, nil, err
	}
	writer, err := windows.CreateNamedPipe(name,
		windows.PIPE_ACCESS_OUTBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_NOWAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, processStdinBufferSize, 0, 0, security)
	if err != nil {
		return nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	reader, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(writer)
		return nil, nil, fmt.Errorf("open stdin pipe reader: %w", err)
	}
	if err := windows.ConnectNamedPipe(writer, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = windows.CloseHandle(writer)
		_ = windows.CloseHandle(reader)
		return nil, nil, fmt.Errorf("connect stdin pipe: %w", err)
	}
	readFile := os.NewFile(uintptr(reader), pipeName)
	cmd.Stdin = readFile
	return &processStdinWriter{handle: writer, done: make(chan struct{})}, readFile, nil
}

func currentUserPipeSecurity() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read stdin pipe owner token: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("restrict stdin pipe to the current user: %w", err)
	}
	attributes := &windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(*attributes)) // Win32 requires the exact ABI size.
	return attributes, nil
}

type processStdinWriter struct {
	mu     sync.Mutex
	handle windows.Handle
	done   chan struct{}
}

func (w *processStdinWriter) Write(data []byte) (int, error) {
	return w.WriteContext(context.Background(), data)
}

func (w *processStdinWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		end := min(len(data), written+processStdinBufferSize)
		n, err := w.writeAvailable(data[written:end])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			if err := w.waitForSpace(ctx); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *processStdinWriter) writeAvailable(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handle == windows.InvalidHandle {
		return 0, os.ErrClosed
	}
	var written uint32
	err := windows.WriteFile(w.handle, data, &written, nil)
	return int(written), err
}

func (w *processStdinWriter) waitForSpace(ctx context.Context) error {
	retry := time.NewTimer(processStdinBackpressureDelay)
	defer retry.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return os.ErrClosed
	case <-retry.C:
		return nil
	}
}

func (w *processStdinWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(w.handle)
	w.handle = windows.InvalidHandle
	close(w.done)
	return err
}
