package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
	if c.session.Connected() {
		t.Fatal("new client must not be connected")
	}
	if c.session.Closed() {
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
	if c.session.Closed() {
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
	srv := newMockCodexServer()
	t.Cleanup(srv.close)
	demux := jsonrpc.NewDemux(jsonrpc.NewLineReader(srv.serverIn), jsonrpc.NewLineWriter(srv.clientOut), c.logger)
	demux.Run(t.Context())
	releaseDispatcher := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDispatcher) }) }
	defer release()
	dispatchCtx, stop := context.WithCancel(t.Context())
	defer stop()
	if err := c.session.StartDispatcher(dispatchCtx, stop, demux, func(_ context.Context, _ *jsonrpc.Demux, done chan struct{}) {
		defer close(done)
		<-releaseDispatcher
	}); err != nil {
		t.Fatal(err)
	}
	c.registerThread(thread)
	compact, err := thread.Compact(t.Context(), nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := compact.Wait(t.Context())
		waitDone <- waitErr
	}()

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
	if thread.isClosed() {
		t.Fatal("thread closed before dispatcher stopped")
	}
	select {
	case err := <-waitDone:
		t.Fatalf("compact subscription ended before dispatcher stopped: %v", err)
	default:
	}

	release()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung after dispatcher stopped")
	}
	if !thread.isClosed() {
		t.Fatal("thread not closed after dispatcher stopped")
	}
	select {
	case err := <-waitDone:
		if err == nil || !strings.Contains(err.Error(), "thread closed before compact completed") {
			t.Fatalf("compact Wait = %v, want thread close", err)
		}
	case <-ctx.Done():
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
while IFS= read -r line; do :; done
`, marker)
	writeClientExecutable(t, helper, script)

	c, err := NewClient(context.Background(), types.NewCodexOptions().WithCLIPath(helper))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		_ = c.Close(closeCtx)
	})

	connectDone := make(chan error, 1)
	go func(client *Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		connectDone <- client.Connect(ctx)
	}(c)

	startupTimeout := time.NewTimer(time.Second)
	defer startupTimeout.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(marker); err == nil && c.ProcessID() != 0 {
			break
		}
		select {
		case <-startupTimeout.C:
			t.Fatal("fake app-server did not start")
		case <-poll.C:
		}
	}

	closeDone := make(chan error, 1)
	go func(client *Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closeDone <- client.Close(ctx)
	}(c)

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
	if c.session.DispatcherDone() != nil {
		select {
		case <-c.session.DispatcherDone():
		default:
			t.Fatal("dispatcher still running after Close")
		}
	}
}

func TestClientConnectUsesDefaultCwdForSubprocess(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cwd")
	helper := filepath.Join(t.TempDir(), "codex-helper")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex 0.144.4"; exit 0; fi
if [ "$1" = "app-server" ]; then shift; fi
pwd > %q
IFS= read -r _initialize
printf '%%s\n' '{"id":1,"result":{}}'
while IFS= read -r line; do :; done
`, marker)
	writeClientExecutable(t, helper, script)

	c, err := NewClient(context.Background(), types.NewCodexOptions().WithCLIPath(helper).WithCwd(cwd))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = c.Close(closeCtx)
	})
	connectDone := make(chan error, 1)
	go func(client *Client, connectCtx context.Context) {
		connectDone <- client.Connect(connectCtx)
	}(c, ctx)
	select {
	case connectErr := <-connectDone:
		if connectErr != nil {
			t.Fatalf("Connect: %v", connectErr)
		}
	case <-ctx.Done():
		t.Fatalf("Connect did not complete: %v", ctx.Err())
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read subprocess cwd marker: %v", err)
	}
	if got := strings.TrimSpace(string(contents)); got != cwd {
		t.Fatalf("subprocess cwd = %q, want %q", got, cwd)
	}
}

func TestClient_InitializeTimeoutIsCLIConnectionError(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "codex-helper")
	secret := "sk-test-secret-in-stderr"
	secretSuffix := "tail-secret-from-long-line"
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex 0.144.4"; exit 0; fi
if [ "$1" = "app-server" ]; then shift; fi
printf 'control protocol initialization failed OPENAI_API_KEY=%s\n' >&2
printf 'DATABASE_URL=postgres://user:%s@localhost/db\n' >&2
printf 'Authorization: Bearer %s%s\n' >&2
while IFS= read -r line; do :; done
`, secret, secret, strings.Repeat("x", 2048), secretSuffix)
	writeClientExecutable(t, helper, script)
	core, logs := observer.New(zap.DebugLevel)
	c, err := NewClient(
		context.Background(),
		types.NewCodexOptions().WithCLIPath(helper).WithLogger(zap.New(core)),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = c.Connect(ctx)
	if err == nil {
		t.Fatal("Connect succeeded; want initialize timeout")
	}
	if !types.IsCLIConnectionError(err) {
		t.Fatalf("Connect error = %T %[1]v, want CLIConnectionError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect error = %v, want context deadline exceeded in chain", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Connect error leaked stderr secret: %v", err)
	}
	if h := c.Health(); h.Ready {
		t.Fatalf("Health.Ready after initialize timeout = true; health=%+v", h)
	}
	entries := logs.FilterMessage("codex.Client.Connect: initialize failed").All()
	if len(entries) == 0 {
		t.Fatal("missing initialize failure diagnostic log")
	}
	fields := entries[0].ContextMap()
	stderrTail, _ := fields["stderr_tail"].(string)
	if stderrTail == "" || strings.Contains(stderrTail, secret) || strings.Contains(stderrTail, secretSuffix) {
		t.Fatalf("stderr diagnostic tail = %q, want non-empty redacted tail", stderrTail)
	}
	if !strings.Contains(stderrTail, "<redacted>") {
		t.Fatalf("stderr diagnostic tail = %q, want redaction marker", stderrTail)
	}
}

func writeClientExecutable(t *testing.T, path, contents string) {
	t.Helper()
	temporaryPath := path + ".tmp"
	if err := os.WriteFile(temporaryPath, []byte(contents), 0o700); err != nil {
		t.Fatalf("write client executable fixture: %v", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		t.Fatalf("publish client executable fixture: %v", err)
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
