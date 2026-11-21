package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
)

const stdinHelperMode = "CODEX_SDK_TEST_STDIN_MODE"
const stdinHelperRelease = "CODEX_SDK_TEST_STDIN_RELEASE"

func TestWindowsProcessStdinHelper(t *testing.T) {
	mode := os.Getenv(stdinHelperMode)
	if mode == "" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	signal := "ready"
	if mode == "partial" {
		if _, err := reader.ReadByte(); err != nil {
			t.Fatal(err)
		}
		signal = "prefix received"
	}
	if _, err := fmt.Fprintln(os.Stdout, signal); err != nil {
		t.Fatal(err)
	}
	waitForStdinRelease(t, os.Getenv(stdinHelperRelease))
	if mode == "partial" {
		return
	}
	first, err := reader.ReadString('\n')
	if err != nil || len(first) != processStdinBufferSize || first[0] != '"' {
		t.Fatalf("initial frame length = %d, error = %v", len(first), err)
	}
	next, err := reader.ReadString('\n')
	if err != nil || next != "\"healthy\"\n" {
		t.Fatalf("next frame = %q, error = %v", next, err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "healthy"); err != nil {
		t.Fatal(err)
	}
}

func waitForStdinRelease(t *testing.T, path string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}

func startStdinHelper(t *testing.T, mode string) (*jsonrpc.LineWriter, *bufio.Scanner, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(t.TempDir(), "read-stdin")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWindowsProcessStdinHelper$")
	cmd.Env = append(os.Environ(), stdinHelperMode+"="+mode, stdinHelperRelease+"="+release)
	proc, err := newSpawnedProc(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proc.closePipes)
	if err := proc.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return jsonrpc.NewLineWriter(proc.stdin), bufio.NewScanner(proc.stdout), release
}

func TestWindowsProcessStdinCancelWithoutBytesPreservesPipe(t *testing.T) {
	t.Parallel()
	writer, child, release := startStdinHelper(t, "full")
	if !child.Scan() || child.Text() != "ready" {
		t.Fatalf("helper readiness = %q (%v)", child.Text(), child.Err())
	}
	const jsonFrameOverhead = 3 // quotes and newline
	writeCtx, finishWrite := context.WithTimeout(t.Context(), 3*time.Second)
	defer finishWrite()
	if err := writer.WriteFrameContext(writeCtx, strings.Repeat("p", processStdinBufferSize-jsonFrameOverhead)); err != nil {
		t.Fatalf("fill pipe with a complete frame: %v", err)
	}
	blockedCtx, stop := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer stop()
	err := writer.WriteFrameContext(blockedCtx, "must not reach peer")
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, jsonrpc.ErrFramePoisoned) {
		t.Fatalf("zero-byte cancellation = %v, want reusable pipe", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrameContext(writeCtx, "healthy"); err != nil {
		t.Fatalf("write after zero-byte cancellation: %v", err)
	}
	if !child.Scan() || child.Text() != "healthy" {
		t.Fatalf("helper did not receive both intact frames: %q (%v)", child.Text(), child.Err())
	}
}

func TestWindowsProcessStdinCancelAfterPrefixPoisonsPipe(t *testing.T) {
	t.Parallel()
	writer, child, _ := startStdinHelper(t, "partial")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- writer.WriteFrameContext(ctx, strings.Repeat("p", 8<<20)) }()
	if !child.Scan() || child.Text() != "prefix received" {
		t.Fatalf("helper prefix = %q (%v)", child.Text(), child.Err())
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, jsonrpc.ErrFramePoisoned) {
			t.Fatalf("partial frame cancellation = %v, want poisoned pipe", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial frame cancellation blocked")
	}
	if err := writer.WriteFrame("next"); !errors.Is(err, jsonrpc.ErrFramePoisoned) {
		t.Fatalf("write after partial frame cancellation = %v", err)
	}
}
