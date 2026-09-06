package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/internal/jsonrpc"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// recordingTransportObserver is a concurrency-safe Observer for asserting
// transport + demux emissions. Embeds NopObserver so only the events under test
// are overridden.
type recordingTransportObserver struct {
	types.NopObserver

	mu        sync.Mutex
	exitCalls int
	exitCode  int
	requested bool
	exitErr   error

	giveUp    atomic.Uint64
	parseErrs atomic.Uint64
}

func (r *recordingTransportObserver) OnSubprocessExit(code int, requested bool, err error) {
	r.mu.Lock()
	r.exitCalls++
	r.exitCode = code
	r.requested = requested
	r.exitErr = err
	r.mu.Unlock()
}

func (r *recordingTransportObserver) OnParseError(n uint, _ error) { r.parseErrs.Store(uint64(n)) }
func (r *recordingTransportObserver) OnParseGiveUp(n uint)         { r.giveUp.Store(uint64(n)) }

func (r *recordingTransportObserver) snapshotExit() (calls, code int, requested bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitCalls, r.exitCode, r.requested, r.exitErr
}

// TestAppServer_OnSubprocessExit_CleanClose proves a Close-initiated shutdown
// emits exactly one OnSubprocessExit with requested=true and no cause.
func TestAppServer_OnSubprocessExit_CleanClose(t *testing.T) {
	t.Parallel()

	obs := &recordingTransportObserver{}
	helper := writeAppServerHelper(t, "while IFS= read -r _line; do :; done\nexit 0")
	app := NewAppServer(AppServerConfig{CLIPath: helper, Observer: obs})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	waitForT(t, func() bool { c, _, _, _ := obs.snapshotExit(); return c >= 1 })
	calls, code, requested, cause := obs.snapshotExit()
	if calls != 1 {
		t.Fatalf("OnSubprocessExit calls = %d, want exactly 1", calls)
	}
	if !requested {
		t.Fatalf("requested = false, want true for Close-initiated exit")
	}
	if cause != nil {
		t.Fatalf("cause = %v, want nil for clean Close", cause)
	}
	if code != 0 {
		t.Fatalf("exitCode = %d, want 0", code)
	}
}

// TestAppServer_OnSubprocessExit_UnexpectedDeath proves a self-death (the
// subprocess exits non-zero on its own, NOT via Close) emits OnSubprocessExit
// with requested=false and a non-nil cause, and surfaces the error via
// GetError + Health.
func TestAppServer_OnSubprocessExit_UnexpectedDeath(t *testing.T) {
	t.Parallel()

	obs := &recordingTransportObserver{}
	// Helper exits non-zero immediately without being asked to.
	helper := writeAppServerHelper(t, "exit 7")
	app := NewAppServer(AppServerConfig{CLIPath: helper, Observer: obs})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })

	waitForT(t, func() bool { c, _, _, _ := obs.snapshotExit(); return c >= 1 })
	calls, code, requested, cause := obs.snapshotExit()
	if calls != 1 {
		t.Fatalf("OnSubprocessExit calls = %d, want exactly 1", calls)
	}
	if requested {
		t.Fatalf("requested = true, want false for self-death")
	}
	if cause == nil {
		t.Fatal("cause = nil, want non-nil for unexpected exit")
	}
	if code != 7 {
		t.Fatalf("exitCode = %d, want 7", code)
	}
	// GetError + Health both surface the death.
	if app.GetError() == nil {
		t.Fatal("GetError() = nil after unexpected death")
	}
	h := app.Health()
	if h.Ready {
		t.Fatalf("Health.Ready = true after death, want false")
	}
	if h.LastError == nil {
		t.Fatal("Health.LastError = nil after death")
	}
}

// TestAppServer_Health_Lifecycle walks Health() through the full lifecycle:
// zero before Connect, Connected+Ready+PID after Connect, not-Ready after Close.
func TestAppServer_Health_Lifecycle(t *testing.T) {
	t.Parallel()

	helper := writeAppServerHelper(t, "while IFS= read -r _line; do :; done\nexit 0")
	app := NewAppServer(AppServerConfig{CLIPath: helper})

	// Before Connect.
	if h := app.Health(); h.Connected || h.Ready || h.PID != 0 || h.LastError != nil {
		t.Fatalf("pre-connect Health = %+v, want zero", h)
	}

	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// After Connect: connected, ready, with a live PID.
	h := app.Health()
	if !h.Connected || !h.Ready {
		t.Fatalf("post-connect Health = %+v, want Connected && Ready", h)
	}
	if h.PID == 0 {
		t.Fatal("post-connect Health.PID = 0, want live pid")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close: not ready.
	waitForT(t, func() bool { return !app.Health().Ready })
	if app.Health().Ready {
		t.Fatal("post-close Health.Ready = true, want false")
	}
}

// TestAppServer_DemuxSurvivesConnectContextCancel proves Connect's ctx is only
// for the connect attempt. The demux read loop is a transport-lifetime task and
// must continue delivering app-server frames until Close.
func TestAppServer_DemuxSurvivesConnectContextCancel(t *testing.T) {
	t.Parallel()

	helper := writeAppServerHelper(t,
		"while IFS= read -r _line; do printf '{\"method\":\"ready\",\"params\":{}}\\n'; done\nexit 0")
	app := NewAppServer(AppServerConfig{CLIPath: helper})
	ctx, cancel := context.WithCancel(context.Background())
	if err := app.Connect(ctx); err != nil {
		cancel()
		t.Fatalf("Connect: %v", err)
	}
	cancel()
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = app.Close(closeCtx)
	})

	if err := app.Demux().Notify("ping", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case message, ok := <-app.Demux().ServerMessages():
		if !ok || message.Notification == nil {
			t.Fatal("notifications channel closed after connect context cancellation")
		}
		note := message.Notification
		if note.Method != "ready" {
			t.Fatalf("note.Method = %q, want ready", note.Method)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for notification after connect context cancellation")
	}
}

// TestAppServer_ParseGiveUpTerminatesSubprocess proves the reliability fix at the
// transport boundary: a helper that streams garbage trips the demux give-up,
// which kills the subprocess (no zombie), emits OnParseGiveUp, surfaces the
// error via GetError, and produces a single OnSubprocessExit.
func TestAppServer_ParseGiveUpTerminatesSubprocess(t *testing.T) {
	t.Parallel()

	obs := &recordingTransportObserver{}
	const threshold uint = 3
	// Helper streams garbage lines forever (until killed) and traps signals so
	// it would NOT exit on its own — only the give-up Kill reaps it.
	helper := writeAppServerHelper(t,
		"trap '' TERM INT\nwhile true; do echo 'garbage-not-json'; sleep 0.01; done")
	app := NewAppServer(AppServerConfig{
		CLIPath:                   helper,
		Observer:                  obs,
		MaxConsecutiveParseErrors: threshold,
	})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })

	// The demux gives up, the transport kills the process, and the watcher emits
	// the single exit.
	waitForT(t, func() bool { c, _, _, _ := obs.snapshotExit(); return c >= 1 })

	if got := obs.giveUp.Load(); got != uint64(threshold) {
		t.Fatalf("OnParseGiveUp consecutive = %d, want %d", got, threshold)
	}
	if app.GetError() == nil {
		t.Fatal("GetError() = nil after parse give-up")
	}
	if !errors.Is(app.GetError(), jsonrpc.ErrParseGiveUp) {
		t.Fatalf("GetError() = %v, want wrapped ErrParseGiveUp", app.GetError())
	}
	calls, _, _, _ := obs.snapshotExit()
	if calls != 1 {
		t.Fatalf("OnSubprocessExit calls = %d, want exactly 1", calls)
	}
}

// TestAppServer_ReadBufferSize_WiredToReader proves the line-cap option flows to
// the demux reader: a single JSON line larger than the 2 MiB default floor but
// within a configured larger ceiling is decoded successfully (would fail with
// the default buffer). This guards against the "option silently ignored" bug.
func TestAppServer_ReadBufferSize_WiredToReader(t *testing.T) {
	t.Parallel()

	const bigBuf = 6 * 1024 * 1024 // 6 MiB ceiling
	// Emit one ~3 MiB JSON notification (over the 2 MiB default, under 6 MiB).
	// head|tr generates the blob in ~30ms; the helper prints the oversized line
	// then reads stdin to stay alive. A frame this large is decoded ONLY when the
	// 6 MiB ceiling is honored; with the 2 MiB default the reader would error
	// with ErrTooLong — this is the proof the line-cap option is wired, not
	// silently ignored.
	helper := writeAppServerHelper(t,
		`blob=$(head -c 3000000 < /dev/zero | tr '\0' 'a')`+"\n"+
			`printf '{"method":"big","params":{"blob":"%s"}}\n' "$blob"`+"\n"+
			"while IFS= read -r _line; do :; done\nexit 0")

	app := NewAppServer(AppServerConfig{CLIPath: helper, ReadBufferSize: bigBuf})
	if err := app.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = app.Close(closeCtx)
	})

	// The oversized notification must arrive intact (decoded, not a read error).
	// 20s tolerates the awk blob build + race-detector + parallel-package CPU
	// contention; the assertion is correctness, not latency.
	select {
	case message, ok := <-app.Demux().ServerMessages():
		if !ok || message.Notification == nil {
			t.Fatal("notifications channel closed before the big frame arrived (line cap not raised?)")
		}
		note := message.Notification
		if note.Method != "big" {
			t.Fatalf("note.Method = %q, want big", note.Method)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the oversized notification")
	}

	// And no read error was recorded.
	if app.GetError() != nil {
		t.Fatalf("GetError() = %v after big frame, want nil", app.GetError())
	}
}

// waitForT polls cond until true or fails after 5s.
func waitForT(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("waitForT: condition never became true within 5s")
		case <-time.After(10 * time.Millisecond): // Sleep: bounded poll for async watcher
		}
	}
}
