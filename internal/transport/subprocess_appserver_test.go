package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestSplitKV(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		wantK  string
		wantV  string
		wantOK bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"FOO=bar=baz", "FOO", "bar=baz", true},
		{"FOO=", "FOO", "", true},
		{"NOEQ", "NOEQ", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			k, v, ok := splitKV(tt.in)
			if k != tt.wantK || v != tt.wantV || ok != tt.wantOK {
				t.Fatalf("got (%q,%q,%v), want (%q,%q,%v)", k, v, ok, tt.wantK, tt.wantV, tt.wantOK)
			}
		})
	}
}

func TestBuildEnv_AddsAndOverrides(t *testing.T) {
	t.Parallel()
	env := buildEnv([]string{"CODEX_TEST_KEY=abc123", "PATH=/override"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CODEX_TEST_KEY=abc123") {
		t.Fatal("expected CODEX_TEST_KEY override to be present")
	}
	// PATH from os.Environ must be overridden.
	pathCount := 0
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected exactly one PATH entry, got %d", pathCount)
	}
	// And the surviving PATH must be our override.
	var gotPath string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			gotPath = e
			break
		}
	}
	if gotPath != "PATH=/override" {
		t.Fatalf("PATH = %q, want %q", gotPath, "PATH=/override")
	}
}

func TestBuildEnv_EmptyValueUnsets(t *testing.T) {
	t.Parallel()
	env := buildEnv([]string{"PATH="})
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			t.Fatalf("PATH should be unset, still have: %q", e)
		}
	}
}

func TestRingBuffer_GrowsToCapThenWrapsAround(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(8)
	writeRingBuffer(t, rb, "ABCDE")
	if got := rb.String(); got != "ABCDE" {
		t.Fatalf("phase-1: %q", got)
	}
	writeRingBuffer(t, rb, "FGHIJKL") // total would be ABCDEFGHIJKL, ring keeps last 8
	if got := rb.String(); got != "EFGHIJKL" {
		t.Fatalf("phase-2 (ring wrap): %q, want %q", got, "EFGHIJKL")
	}
	writeRingBuffer(t, rb, "MNOP")
	if got := rb.String(); got != "IJKLMNOP" {
		t.Fatalf("phase-3 (continued wrap): %q, want %q", got, "IJKLMNOP")
	}
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(8)
	writeRingBuffer(t, rb, "ABCDEFGH")
	if got := rb.String(); got != "ABCDEFGH" {
		t.Fatalf("got %q, want %q", got, "ABCDEFGH")
	}
}

func TestRingBuffer_WriteLargerThanSize(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(4)
	writeRingBuffer(t, rb, "ABCDEFGHIJ")
	if got := rb.String(); got != "GHIJ" {
		t.Fatalf("got %q, want %q", got, "GHIJ")
	}
}

func TestRingBuffer_FinalByteSentinelPreserved(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(8)
	input := "prefix-0123456789-SENTINEL_Z"
	want := input[len(input)-8:]
	writeRingBuffer(t, rb, input)
	if got := rb.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.HasSuffix(rb.String(), "Z") {
		t.Fatalf("final sentinel byte was not preserved: %q", rb.String())
	}
}

func writeRingBuffer(t *testing.T, rb *ringBuffer, value string) {
	t.Helper()
	n, err := rb.Write([]byte(value))
	if err != nil {
		t.Fatalf("ringBuffer.Write(%q): %v", value, err)
	}
	if n != len(value) {
		t.Fatalf("ringBuffer.Write(%q) wrote %d bytes, want %d", value, n, len(value))
	}
}

func TestRingBuffer_EmptyString(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(4)
	if got := rb.String(); got != "" {
		t.Fatalf("empty ring buffer should be empty string, got %q", got)
	}
}

// sanity: ringBuffer implements io.Writer
func TestRingBuffer_AsWriter(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(16)
	b := bytes.NewBufferString("hello world")
	n, err := b.WriteTo(rb)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("WriteTo wrote %d, want 11", n)
	}
	if rb.String() != "hello world" {
		t.Fatalf("got %q", rb.String())
	}
}

func TestAppServerClassifyExitShutdownRequestedSuppressesExitError(t *testing.T) {
	t.Parallel()
	err := exec.Command("sh", "-c", "exit 7").Run()
	if err == nil {
		t.Fatal("expected command to fail")
	}

	app := &AppServer{}
	if got := app.classifyExit(err, true); got != nil {
		t.Fatalf("classifyExit(shutdownRequested=true) = %v, want nil", got)
	}
	if got := app.classifyExit(err, false); got == nil || !strings.Contains(got.Error(), "exit=7") {
		t.Fatalf("classifyExit(shutdownRequested=false) = %v, want process error with exit=7", got)
	}
}

func writeAppServerHelper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-helper")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo \"codex 0.130.0\"; exit 0; fi\n" +
		"if [ \"$1\" = \"app-server\" ]; then shift; fi\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatalf("chmod helper: %v", err)
	}
	return path
}

func TestAppServerStderrTailStableAfterClose(t *testing.T) {
	t.Parallel()

	chunk := "0123456789abcdef"
	repeatCount := StderrRingSize/len(chunk) + 32
	sentinel := "TAIL_SENTINEL_codex_close_issue_79"
	payload := strings.Repeat(chunk, repeatCount) + sentinel
	stderr := payload + "\n"
	want := stderr[len(stderr)-StderrRingSize:]
	helper := writeAppServerHelper(t, fmt.Sprintf(
		"while IFS= read -r _line; do :; done\ni=0\nwhile [ \"$i\" -lt %d ]; do printf '%s' >&2; i=$((i + 1)); done\nprintf '%%s\\n' '%s' >&2\nexit 0",
		repeatCount,
		chunk,
		sentinel,
	))

	app := NewAppServer(AppServerConfig{CLIPath: helper})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	gotFirst := app.Stderr()
	if gotFirst != want {
		t.Fatalf(
			"Stderr() mismatch: got len=%d suffix=%q; want len=%d suffix=%q; first diff at byte %d",
			len(gotFirst), suffixForLog(gotFirst, 96), len(want), suffixForLog(want, 96), firstDiffIndex(gotFirst, want),
		)
	}
	gotSecond := app.Stderr()
	if gotSecond != gotFirst {
		t.Fatalf(
			"Stderr() changed after close: first len=%d suffix=%q; second len=%d suffix=%q; first diff at byte %d",
			len(gotFirst), suffixForLog(gotFirst, 96), len(gotSecond), suffixForLog(gotSecond, 96), firstDiffIndex(gotSecond, gotFirst),
		)
	}
}

func TestAppServerDrainStderrWaitsForDone(t *testing.T) {
	done := make(chan struct{})
	returned := make(chan struct{})
	app := &AppServer{}

	go func() {
		app.drainStderr(done)
		close(returned)
	}()

	// drainStderr waits for done, but is now bounded by StderrDrainTimeout so a
	// descendant holding the stderr write-end can't wedge Close forever. Probe
	// well under that bound: with done still open, the drain must still be
	// blocked. (The bounded-return-on-timeout path is covered separately by
	// TestDrainStderrBoundedWhenPipeNeverEOFs.)
	select {
	case <-returned:
		t.Fatal("drainStderr returned before stderrDone closed and before its timeout")
	case <-time.After(StderrDrainTimeout / 5):
	}

	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("drainStderr did not return after stderrDone closed")
	}
}

func firstDiffIndex(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return limit
	}
	return -1
}

func suffixForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func TestAppServerCloseGracefulAndIdempotent(t *testing.T) {
	t.Parallel()

	helper := writeAppServerHelper(t, "while IFS= read -r _line; do :; done\nexit 0")
	app := NewAppServer(AppServerConfig{CLIPath: helper})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}
	if err := app.Close(closeCtx); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
}

func TestAppServerCloseCancellationDoesNotHang(t *testing.T) {
	t.Parallel()

	helper := writeAppServerHelper(t, "trap '' INT TERM\nwhile true; do sleep 1; done")
	app := NewAppServer(AppServerConfig{CLIPath: helper})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	closeCtx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- app.Close(closeCtx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close(canceled ctx) returned %v, want nil for shutdown-requested exit", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close(canceled ctx) hung")
	}
}

func TestAppServerClassifyExitUnexpectedIncludesTypedProcessError(t *testing.T) {
	t.Parallel()

	err := exec.Command("sh", "-c", "exit 7").Run()
	if err == nil {
		t.Fatal("expected command to fail")
	}
	app := &AppServer{stderr: newRingBuffer(StderrRingSize)}
	_, _ = app.stderr.Write([]byte("codex stderr tail"))

	got := app.classifyExit(err, false)
	var procErr *types.ProcessError
	if !errors.As(got, &procErr) {
		t.Fatalf("classifyExit = %T %v, want *types.ProcessError", got, got)
	}
	if procErr.ExitCode != 7 || procErr.Stderr != "codex stderr tail" {
		t.Fatalf("ProcessError = %+v, want exit=7 with stderr tail", procErr)
	}
}
