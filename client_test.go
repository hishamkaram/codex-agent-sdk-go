package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
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

func TestClient_ProcessID_ZeroBeforeConnect(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.ProcessID(); got != 0 {
		t.Fatalf("ProcessID before Connect = %d, want 0", got)
	}
}

func TestClient_SessionID_EmptyBeforeAnyThread(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.SessionID(); got != "" {
		t.Fatalf("SessionID with no threads = %q, want empty", got)
	}
}

func TestClient_SessionID_TracksLatestRegisteredThread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ops     []threadOp
		wantSID string
	}{
		{
			name: "single register",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
			},
			wantSID: "t1",
		},
		{
			name: "two registers — second wins",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opRegister, id: "t2"},
			},
			wantSID: "t2",
		},
		{
			name: "register then unregister matching id clears",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opUnregister, id: "t1"},
			},
			wantSID: "",
		},
		{
			name: "unregister of non-latest id keeps latest",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opRegister, id: "t2"},
				{kind: opUnregister, id: "t1"},
			},
			wantSID: "t2",
		},
		{
			name: "register / unregister / register — final wins",
			ops: []threadOp{
				{kind: opRegister, id: "t1"},
				{kind: opUnregister, id: "t1"},
				{kind: opRegister, id: "t2"},
			},
			wantSID: "t2",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(context.Background(), types.NewCodexOptions())
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			for _, op := range tt.ops {
				switch op.kind {
				case opRegister:
					c.registerThread(&Thread{client: c, id: op.id})
				case opUnregister:
					c.session.Threads().Archive(op.id)
				}
			}
			if got := c.SessionID(); got != tt.wantSID {
				t.Fatalf("SessionID = %q, want %q", got, tt.wantSID)
			}
		})
	}
}

func TestClient_SessionID_ClearedByClose(t *testing.T) {
	t.Parallel()
	c, err := NewClient(context.Background(), types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.registerThread(&Thread{client: c, id: "t1"})
	if got := c.SessionID(); got != "t1" {
		t.Fatalf("SessionID after register = %q, want %q", got, "t1")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := c.SessionID(); got != "" {
		t.Fatalf("SessionID after Close = %q, want empty", got)
	}
}

type threadOpKind int

const (
	opRegister threadOpKind = iota
	opUnregister
)

type threadOp struct {
	kind threadOpKind
	id   string
}

// clientRecordingObserver records the client-layer events under test.
type clientRecordingObserver struct {
	types.NopObserver

	connectN   atomic.Uint64
	connectErr atomic.Bool // true if the last OnConnect carried a non-nil error

	mu      sync.Mutex
	unknown []string
}

func (o *clientRecordingObserver) OnConnect(_ time.Duration, err error) {
	o.connectN.Add(1)
	o.connectErr.Store(err != nil)
}

func (o *clientRecordingObserver) OnUnknownMessage(d string) {
	o.mu.Lock()
	o.unknown = append(o.unknown, d)
	o.mu.Unlock()
}

func (o *clientRecordingObserver) unknownSnapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.unknown...)
}

// TestClient_OnConnect_FiresOnFailure proves OnConnect is emitted once when
// Connect fails (here: a bogus CLI path that cannot spawn). The error arg is
// non-nil.
func TestClient_OnConnect_FiresOnFailure(t *testing.T) {
	t.Parallel()

	obs := &clientRecordingObserver{}
	opts := types.NewCodexOptions().
		WithCLIPath("/nonexistent/codex-binary-xyz").
		WithObserver(obs)
	c, err := NewClient(context.Background(), opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if connErr := c.Connect(ctx); connErr == nil {
		t.Fatal("Connect with bogus CLI path unexpectedly succeeded")
		_ = c.Close(context.Background())
	}

	if got := obs.connectN.Load(); got != 1 {
		t.Fatalf("OnConnect call count = %d, want 1", got)
	}
	if !obs.connectErr.Load() {
		t.Fatal("OnConnect error arg was nil, want non-nil for a failed connect")
	}
}

// TestClient_OnConnect_NotEmittedOnDuplicateConnect proves the early-return
// guards (already-connected / closed) do NOT emit OnConnect — they are not real
// connect attempts.
func TestClient_OnConnect_NotEmittedOnDuplicateConnect(t *testing.T) {
	t.Parallel()

	obs := &clientRecordingObserver{}
	// Use the mock client which marks connected=true without going through
	// Connect. A second Connect must short-circuit without emitting.
	c, _ := setupMockClient(t, types.NewCodexOptions().WithObserver(obs), nil)

	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("second Connect on an already-connected client should error")
	}
	if got := obs.connectN.Load(); got != 0 {
		t.Fatalf("OnConnect fired %d times on a duplicate Connect, want 0", got)
	}
}

// TestClient_Health_NilTransport proves Client.Health returns a zero snapshot
// when there is no transport (e.g. the mock-client path, or before Connect).
func TestClient_Health_NilTransport(t *testing.T) {
	t.Parallel()

	c, _ := setupMockClient(t, types.NewCodexOptions(), nil)
	h := c.Health()
	if h.Connected || h.Ready || h.PID != 0 || h.LastError != nil {
		t.Fatalf("Health with nil transport = %+v, want zero", h)
	}
}

// TestClient_OnUnknownMessage_OnUnknownEvent proves the Client emits
// OnUnknownMessage when codex sends a notification method the SDK does not
// recognize (the events parser yields an *UnknownEvent).
func TestClient_OnUnknownMessage_OnUnknownEvent(t *testing.T) {
	t.Parallel()

	obs := &clientRecordingObserver{}
	_, srv := setupMockClient(t, types.NewCodexOptions().WithObserver(obs), nil)

	// Push a notification with a method that maps to UnknownEvent. It carries a
	// threadId so it is not dropped as unroutable before observation — the
	// observer fires in handleNotification before routing.
	srv.push(notify("turn/somethingTheSDKDoesNotKnow", map[string]any{
		"threadId": "t1",
	}))

	deadline := time.After(3 * time.Second)
	for {
		if got := obs.unknownSnapshot(); len(got) >= 1 {
			if got[0] != "turn/somethingTheSDKDoesNotKnow" {
				t.Fatalf("unknown discriminator = %q, want the unknown method name", got[0])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("OnUnknownMessage never fired for an unknown notification method")
		case <-time.After(5 * time.Millisecond): // Sleep: bounded poll for async dispatcher
		}
	}
}

func TestReplacementEnvironmentAcrossPublicLaunchAndProbes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("connected schema probes require Linux runtime identity proof")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SDK57_PUBLIC_PARENT_ONLY", "parent")
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement(
			"CODEX_HOME=/caller/codex-home",
			"CODEX_SDK_HOOK_SOCKET=/caller/socket",
			"SDK57_PUBLIC_SELECTION=old",
			"GORACE=atexit_sleep_ms=0",
		).
		WithEnv("SDK57_PUBLIC_SELECTION=new").
		WithHookCallback(types.DefaultAllowHookHandler).
		WithShimPath(fixture.executable)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := DiscoverRuntimeControls(ctx, opts, nil); err != nil {
		t.Fatal(err)
	}
	opts.WithExperimentalAPI(true)
	if _, err := DiscoverRuntimeFeatures(ctx, opts); err != nil {
		t.Fatal(err)
	}
	before := readLaunchEnvironments(t, fixture.launchEnv)
	for _, record := range before {
		if record.home != "/caller/codex-home" || record.socket != "/caller/socket" {
			t.Fatalf("standalone %s inherited a connection hook environment: %+v", record.stage, record)
		}
	}
	assertProbeStages(t, before, "--help", "--version", "app-server generate-json-schema")

	_, thread := connectTurnProgramFixture(t, opts)
	opts.WithEnvReplacement("SDK57_PUBLIC_SELECTION=changed-after-connect")
	program := types.CyberAccessProgramStandard
	if _, err := thread.Run(ctx, "synthetic probe", &types.RunOptions{CyberAccessProgram: &program}); err != nil {
		t.Fatal(err)
	}
	connected := readLaunchEnvironments(t, fixture.launchEnv)[len(before):]
	assertProbeStages(t, connected, "--version", "app-server --enable", "app-server generate-json-schema")
	for _, record := range connected {
		if record.home != connected[0].home || record.socket != connected[0].socket {
			t.Fatalf("connected %s read different hook additions: %+v", record.stage, record)
		}
	}
	codexHome, socket := connected[0].home, connected[0].socket
	if !filepath.IsAbs(codexHome) || codexHome == "/caller/codex-home" || socket == "/caller/socket" {
		t.Fatalf("SDK hook variables did not override caller entries: %+v", connected[0])
	}
	if _, err := os.Stat(filepath.Join(codexHome, "hooks.json")); err != nil {
		t.Fatalf("isolated hook file: %v", err)
	}
	if info, err := os.Stat(socket); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("hook socket: info=%v err=%v", info, err)
	}
	if got := os.Getenv("SDK57_PUBLIC_PARENT_ONLY"); got != "parent" {
		t.Fatalf("parent environment was mutated: %q", got)
	}
}

type launchEnvironmentRecord struct {
	stage       string
	home        string
	socket      string
	processHome string
}

func readLaunchEnvironments(t *testing.T, path string) []launchEnvironmentRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []launchEnvironmentRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 7 || fields[4] != "new" || fields[5] != "unset" {
			t.Fatalf("child command did not receive exact replacement: %q", line)
		}
		records = append(records, launchEnvironmentRecord{
			stage: strings.TrimSpace(fields[0] + " " + fields[1]),
			home:  fields[2], socket: fields[3], processHome: fields[6],
		})
	}
	return records
}

func assertProbeStages(t *testing.T, records []launchEnvironmentRecord, stages ...string) {
	t.Helper()
	for _, stage := range stages {
		found := false
		for _, record := range records {
			found = found || record.stage == stage
		}
		if !found {
			t.Errorf("did not observe CLI stage %q in %+v", stage, records)
		}
	}
}

func TestStandaloneProbesIgnoreLaunchDirectoryAndTrimCLIPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	t.Parallel()
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath("  " + fixture.executable + "  ").
		WithCwd(filepath.Join(t.TempDir(), "does-not-exist")).
		WithEnvReplacement("GORACE=atexit_sleep_ms=0")
	if _, err := DiscoverRuntimeControls(t.Context(), opts, nil); err != nil {
		t.Fatalf("standalone controls: %v", err)
	}
	opts.WithExperimentalAPI(true)
	if _, err := DiscoverRuntimeFeatures(t.Context(), opts); err != nil {
		t.Fatalf("standalone features: %v", err)
	}
}

func TestConflictingUserHookHomeRejectedBeforeConfigWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	const original = `{"original":true}`
	if err := os.WriteFile(hooksPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement("CODEX_HOME=" + t.TempDir()).
		WithHookCallback(types.DefaultAllowHookHandler).
		WithHookConfigMode(types.HookConfigModeUserHome).
		WithShimPath(fixture.executable)
	client, err := NewClient(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	connectErr := client.Connect(t.Context())
	if connectErr == nil || !strings.Contains(connectErr.Error(), "user-home hooks require CODEX_HOME") {
		t.Fatalf("hook conflict error = %v", connectErr)
	}
	current, readErr := os.ReadFile(hooksPath)
	if readErr != nil || string(current) != original {
		t.Fatalf("existing hook config changed before conflict was rejected: %v", readErr)
	}
	if _, probeErr := os.Stat(fixture.launchEnv); !os.IsNotExist(probeErr) {
		t.Fatalf("hook conflict started a CLI process: %v", probeErr)
	}
}

func TestUserHomeHookConfigPinnedAcrossChildHomeReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a symlink to the test executable")
	}
	home, childHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	const original = `{"original":true}`
	if err := os.WriteFile(hooksPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	fixture := writeTurnProgramCLI(t, turnProgramSchema)
	opts := types.NewCodexOptions().
		WithCLIPath(fixture.executable).
		WithEnvReplacement("HOME="+childHome, "SDK57_PUBLIC_SELECTION=new", "GORACE=atexit_sleep_ms=0").
		WithHookCallback(types.DefaultAllowHookHandler).
		WithHookConfigMode(types.HookConfigModeUserHome).
		WithShimPath(fixture.executable)
	client, _ := connectTurnProgramFixture(t, opts)
	if installed, err := os.ReadFile(hooksPath); err != nil || string(installed) == original {
		t.Fatalf("user-home hooks were not installed: %v", err)
	}
	records := readLaunchEnvironments(t, fixture.launchEnv)
	assertProbeStages(t, records, "--version", "app-server --enable")
	for _, record := range records {
		if record.home != codexHome || record.processHome != childHome {
			t.Fatalf("%s: CODEX_HOME=%q HOME=%q, want %q and %q", record.stage, record.home, record.processHome, codexHome, childHome)
		}
	}

	if err := client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if restored, err := os.ReadFile(hooksPath); err != nil || string(restored) != original {
		t.Fatalf("Close did not restore the original hooks file: %v", err)
	}
}
