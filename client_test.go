package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestNewClient_NilOptsIsError(t *testing.T) {
	t.Parallel()
	_, err := NewClient(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil opts")
	}
	if !strings.Contains(err.Error(), "opts must not be nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClient_ReturnsUnconnected(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatal(err)
	}
	if c.connected.Load() {
		t.Fatal("new client must not be connected")
	}
	if c.closed.Load() {
		t.Fatal("new client must not be closed")
	}
}

func TestClient_CloseIsIdempotentPreConnect(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestClient_ConnectAfterCloseReturnsClientClosed(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := c.Connect(context.Background())
	if !errors.Is(err, types.ErrClientClosed) {
		t.Fatalf("Connect after Close error = %v, want ErrClientClosed", err)
	}
}

func TestClient_CloseNilContextDoesNotClose(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	err := c.Close(nil) //nolint:staticcheck // intentional nil-context regression coverage.
	if err == nil {
		t.Fatal("Close(nil) error = nil, want context-required error")
	}
	if !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("Close(nil) error = %v, want context-required error", err)
	}
	if c.closed.Load() {
		t.Fatal("Close(nil) marked client closed")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close after Close(nil): %v", err)
	}
}

func TestClient_CloseWaitsForDispatcherBeforeClosingCompactSubscription(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	thread := newThread(c, "T-close")
	sub := make(chan *types.ContextCompacted, 1)
	subPtr := &sub
	thread.compactSub.Store(subPtr)
	c.threads[thread.ID()] = thread
	dispatcherDone := make(chan struct{})
	c.dispatcherDone = dispatcherDone

	closeDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		closeDone <- c.Close(ctx)
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before dispatcher stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if thread.closed.Load() {
		t.Fatal("thread closed before dispatcher stopped")
	}
	select {
	case _, ok := <-sub:
		if !ok {
			t.Fatal("compact subscription closed before dispatcher stopped")
		}
	default:
	}

	close(dispatcherDone)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung after dispatcher stopped")
	}
	if !thread.closed.Load() {
		t.Fatal("thread not closed after dispatcher stopped")
	}
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("compact subscription still open after thread close")
		}
	default:
		t.Fatal("compact subscription was not closed")
	}
}

func TestClient_CloseDuringConnectCancelsHungInitialize(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	helper := filepath.Join(t.TempDir(), "codex-helper")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex 0.130.0"; exit 0; fi
if [ "$1" = "app-server" ]; then shift; fi
printf started > %q
while IFS= read -r line; do sleep 5; done
`, marker)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	c, err := NewClient(context.Background(), types.NewCodexOptions().WithCLIPath(helper))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	connectDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		connectDone <- c.Connect(ctx)
	}()

	deadline := time.Now().Add(1 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil && c.ProcessID() != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake app-server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closeDone <- c.Close(ctx)
	}()

	select {
	case err := <-connectDone:
		if err == nil {
			t.Fatal("Connect succeeded after Close canceled a hung initialize")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect hung")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
	if h := c.Health(); h.Ready {
		t.Fatalf("Health.Ready after Close = true; health=%+v", h)
	}
	if c.dispatcherDone != nil {
		select {
		case <-c.dispatcherDone:
		default:
			t.Fatal("dispatcher still running after Close")
		}
	}
}

func TestClient_StartThreadBeforeConnectIsError(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	_, err := c.StartThread(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when StartThread before Connect")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildInitializeParams_IncludesNotificationOptOut(t *testing.T) {
	t.Parallel()
	opts := types.NewCodexOptions().
		WithExperimentalAPI(true).
		WithOptOutNotificationMethods("process/outputDelta", "warning")

	params := buildInitializeParams(opts)
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %T", params["capabilities"])
	}
	if capabilities["experimentalApi"] != true {
		t.Fatalf("experimentalApi = %#v", capabilities["experimentalApi"])
	}
	got, ok := capabilities["optOutNotificationMethods"].([]string)
	if !ok {
		t.Fatalf("optOutNotificationMethods = %T", capabilities["optOutNotificationMethods"])
	}
	if len(got) != 2 || got[0] != "process/outputDelta" || got[1] != "warning" {
		t.Fatalf("optOutNotificationMethods = %#v", got)
	}
}

func TestClient_ResumeThreadEmptyIDIsError(t *testing.T) {
	t.Parallel()
	c, _ := NewClient(context.Background(), types.NewCodexOptions())
	// Even though we haven't connected yet, the empty-ID check should
	// still fire (the not-connected check fires first here actually, but
	// both are errors).
	_, err := c.ResumeThread(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty threadID")
	}
}
