package codex

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

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
