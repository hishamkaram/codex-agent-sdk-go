//go:build integration

// Real-CLI observability integration test. Connects to the REAL codex
// app-server with an Observer wired and asserts the connect-path telemetry
// fires and Health() reflects a live subprocess. No turns, no billing.
//
// Run:
//
//	go test -tags=integration -race -run TestIntegration_ObserverConnectAndHealth ./tests/...
package tests

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// integrationObserver records connect + first-message + exit telemetry from a
// real codex app-server. Embeds NopObserver for forward compatibility.
type integrationObserver struct {
	types.NopObserver

	connectN    atomic.Uint64
	connectOK   atomic.Bool // last OnConnect had nil error
	firstMsgN   atomic.Uint64
	exitN       atomic.Uint64
	exitReqLast atomic.Bool

	mu      sync.Mutex
	lastDur time.Duration
}

func (o *integrationObserver) OnConnect(d time.Duration, err error) {
	o.connectN.Add(1)
	o.connectOK.Store(err == nil)
	o.mu.Lock()
	o.lastDur = d
	o.mu.Unlock()
}

func (o *integrationObserver) OnFirstMessage(time.Duration) { o.firstMsgN.Add(1) }

func (o *integrationObserver) OnSubprocessExit(_ int, requested bool, _ error) {
	o.exitN.Add(1)
	o.exitReqLast.Store(requested)
}

// TestIntegration_ObserverConnectAndHealth wires an Observer to a real codex
// client, connects, and asserts:
//   - OnConnect fired exactly once with a nil error
//   - Health() reports Connected + Ready + a live PID
//   - after a clean Close, OnSubprocessExit fired once with requested=true
func TestIntegration_ObserverConnectAndHealth(t *testing.T) {
	requireAuth(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	obs := &integrationObserver{}
	client, err := codex.NewClient(ctx, integrationOptions(t).
		WithVerbose(false).
		WithObserver(obs))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// OnConnect fired exactly once, with success.
	if got := obs.connectN.Load(); got != 1 {
		t.Fatalf("OnConnect call count = %d, want 1", got)
	}
	if !obs.connectOK.Load() {
		t.Fatal("OnConnect carried a non-nil error against the real CLI")
	}
	obs.mu.Lock()
	dur := obs.lastDur
	obs.mu.Unlock()
	if dur <= 0 {
		t.Fatalf("OnConnect duration = %v, want > 0", dur)
	}

	// Health reflects a live subprocess.
	h := client.Health()
	if !h.Connected {
		t.Fatalf("Health.Connected = false after Connect: %+v", h)
	}
	if !h.Ready {
		t.Fatalf("Health.Ready = false after Connect: %+v", h)
	}
	if h.PID == 0 {
		t.Fatalf("Health.PID = 0 after Connect: %+v", h)
	}
	if h.LastError != nil {
		t.Fatalf("Health.LastError = %v after a clean Connect", h.LastError)
	}
	t.Logf("connected: pid=%d connect_dur=%v first_msgs=%d", h.PID, dur, obs.firstMsgN.Load())

	// Clean shutdown emits a single requested exit.
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for obs.exitN.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("OnSubprocessExit never fired after Close")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := obs.exitN.Load(); got != 1 {
		t.Fatalf("OnSubprocessExit call count = %d, want 1", got)
	}
	if !obs.exitReqLast.Load() {
		t.Fatal("OnSubprocessExit requested = false after a clean Close, want true")
	}

	// Post-close Health is not ready.
	if h := client.Health(); h.Ready {
		t.Fatalf("post-close Health.Ready = true: %+v", h)
	}
}
