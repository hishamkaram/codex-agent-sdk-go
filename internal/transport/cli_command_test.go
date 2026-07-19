package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRetryOnETXTBSYRetriesLockedExecutable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "codex")
	writeExecutableFixture(t, path, "#!/bin/sh\nprintf '%s\\n' 'codex 9.1.0'\n")

	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open executable for write: %v", err)
	}
	writerClosed := false
	t.Cleanup(func() {
		if !writerClosed {
			_ = writer.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	attempts := 0
	var releaseErr error
	err = retryOnETXTBSY(ctx, func() error {
		attempts++
		runErr := exec.CommandContext(ctx, path, "--version").Run()
		if errors.Is(runErr, syscall.ETXTBSY) && !writerClosed {
			releaseErr = writer.Close()
			writerClosed = true
		}
		return runErr
	}, nil)
	if releaseErr != nil {
		t.Fatalf("release executable write lock: %v", releaseErr)
	}
	if err != nil {
		t.Fatalf("retryOnETXTBSY() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("command attempts = %d, want 2", attempts)
	}
}

func TestRetryOnETXTBSYStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryOnETXTBSY(ctx, func() error {
		attempts++
		cancel()
		return syscall.ETXTBSY
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryOnETXTBSY() error = %v, want context canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("command attempts = %d, want 1", attempts)
	}
}

func TestRetryOnETXTBSYDoesNotRetryOtherFailures(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("CLI rejected command")
	attempts := 0
	err := retryOnETXTBSY(context.Background(), func() error {
		attempts++
		return wantErr
	}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("retryOnETXTBSY() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("command attempts = %d, want 1", attempts)
	}
}

func TestRetryOnETXTBSYGivesUpAfterBoundedAttempts(t *testing.T) {
	t.Parallel()

	attempts := 0
	retries := 0
	err := retryOnETXTBSY(context.Background(), func() error {
		attempts++
		return syscall.ETXTBSY
	}, func(int) {
		retries++
	})
	if !errors.Is(err, syscall.ETXTBSY) {
		t.Fatalf("retryOnETXTBSY() error = %v, want ETXTBSY", err)
	}
	if attempts != maxCLICommandAttempts {
		t.Fatalf("command attempts = %d, want %d", attempts, maxCLICommandAttempts)
	}
	if retries != maxCLICommandAttempts-1 {
		t.Fatalf("retry callbacks = %d, want %d", retries, maxCLICommandAttempts-1)
	}
}
