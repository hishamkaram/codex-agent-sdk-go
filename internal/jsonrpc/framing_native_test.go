package jsonrpc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const nativePipeHelperKey = "SDK57_NATIVE_PIPE_HELPER"

func TestNativeSubprocessPipeHelper(t *testing.T) {
	if os.Getenv(nativePipeHelperKey) != "1" {
		return
	}
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	// The first byte came from the pending frame. Signal this once, then
	// deliberately leave the rest of the frame blocked on the OS pipe.
	if _, err := fmt.Fprintln(os.Stdout, "partial frame"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

func TestNativeSubprocessPipeCancelAbortsPartialFrame(t *testing.T) {
	t.Parallel()
	watchdog, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(watchdog, self, "-test.run=^TestNativeSubprocessPipeHelper$")
	cmd.Env = append(os.Environ(), nativePipeHelperKey+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	writer := NewLineWriter(stdin)
	ctx, cancel := context.WithCancel(watchdog)
	defer cancel()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.WriteFrameContext(ctx, strings.Repeat("p", 8<<20))
	}()
	defer func() {
		cancel()
		_ = writer.Close()
	}()

	// The buffer is much larger than native anonymous pipe buffers. The
	// helper signals after the write starts and never drains another byte.
	barrier := bufio.NewScanner(stdout)
	if !barrier.Scan() || barrier.Text() != "partial frame" {
		t.Fatalf("helper did not read the first byte: %v", barrier.Err())
	}
	cancel()
	select {
	case err := <-writeDone:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrFramePoisoned) {
			t.Fatalf("canceled partial frame error = %v", err)
		}
	case <-watchdog.Done():
		t.Fatalf("native write did not stop after cancellation: %v", watchdog.Err())
	}
	if err := writer.WriteFrame(42); err == nil {
		t.Fatal("subsequent frame unexpectedly accepted on poisoned pipe")
	}
}
