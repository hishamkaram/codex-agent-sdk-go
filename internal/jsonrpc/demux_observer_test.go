package jsonrpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// recordingObserver is a concurrency-safe Observer for asserting demux
// emissions. It embeds NopObserver so it only overrides the events under test.
type recordingObserver struct {
	types.NopObserver

	firstMsg     atomic.Uint64 // count of OnFirstMessage calls
	parseErrLast atomic.Uint64 // last consecutive count from OnParseError
	parseErrN    atomic.Uint64 // count of OnParseError calls
	giveUp       atomic.Uint64 // last consecutive count from OnParseGiveUp
	giveUpN      atomic.Uint64 // count of OnParseGiveUp calls
	backpressure atomic.Uint64 // count of OnBackpressure calls

	mu      sync.Mutex
	unknown []string // discriminators from OnUnknownMessage
}

func (r *recordingObserver) OnFirstMessage(time.Duration) { r.firstMsg.Add(1) }

func (r *recordingObserver) OnParseError(n uint, _ error) {
	r.parseErrLast.Store(uint64(n))
	r.parseErrN.Add(1)
}

func (r *recordingObserver) OnParseGiveUp(n uint) {
	r.giveUp.Store(uint64(n))
	r.giveUpN.Add(1)
}

func (r *recordingObserver) OnBackpressure() { r.backpressure.Add(1) }

func (r *recordingObserver) OnUnknownMessage(d string) {
	r.mu.Lock()
	r.unknown = append(r.unknown, d)
	r.mu.Unlock()
}

func (r *recordingObserver) unknownCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.unknown)
}

// makeDemuxWithObserver wires a Demux to a mockServer with the given options.
func makeDemuxWithObserver(t *testing.T, opts ...DemuxOption) (*Demux, *mockServer) {
	t.Helper()
	s := newMockServer()
	lr := NewLineReader(s.serverIn)
	lw := NewLineWriter(s.clientOut)
	d := NewDemux(lr, lw, nil, opts...)
	d.Run(context.Background())
	return d, s
}

// TestDemux_OnFirstMessage fires exactly once, on the first decoded frame.
func TestDemux_OnFirstMessage(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	d, s := makeDemuxWithObserver(t, WithObserver(obs))
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// Two notifications — OnFirstMessage must fire only once. The size-64
	// notifications channel buffers both without draining.
	s.sendToClient(t, `{"method":"turn/started","params":{"threadId":"t1"}}`)
	s.sendToClient(t, `{"method":"turn/completed","params":{"threadId":"t1"}}`)

	// Both notifications must arrive (proving the loop processed two frames),
	// then assert OnFirstMessage fired exactly once.
	if got := len(drainBlocking(t, d.Notifications(), 2)); got != 2 {
		t.Fatalf("received %d notifications, want 2", got)
	}
	waitFor(t, func() bool { return obs.firstMsg.Load() >= 1 })
	if got := obs.firstMsg.Load(); got != 1 {
		t.Fatalf("OnFirstMessage count = %d, want 1", got)
	}
}

// TestDemux_OnParseError_CountsConsecutive proves each malformed line increments
// the running consecutive count and a subsequent valid frame resets it.
func TestDemux_OnParseError_CountsConsecutive(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	// High threshold so we don't give up during this test.
	d, s := makeDemuxWithObserver(t, WithObserver(obs), WithMaxParseErrors(100))
	defer func() {
		_ = d.Close()
		s.close()
	}()

	s.sendToClient(t, `not json 1`)
	s.sendToClient(t, `not json 2`)
	s.sendToClient(t, `not json 3`)
	waitFor(t, func() bool { return obs.parseErrN.Load() >= 3 })
	if got := obs.parseErrLast.Load(); got != 3 {
		t.Fatalf("last consecutive parse-error count = %d, want 3", got)
	}

	// A valid frame resets the counter; the next bad line should report 1.
	s.sendToClient(t, `{"method":"turn/started","params":{"threadId":"t1"}}`)
	s.sendToClient(t, `bad again`)
	waitFor(t, func() bool { return obs.parseErrN.Load() >= 4 && obs.parseErrLast.Load() == 1 })
	if got := obs.parseErrLast.Load(); got != 1 {
		t.Fatalf("after reset, consecutive count = %d, want 1", got)
	}
}

// TestDemux_GiveUpTerminates proves the reliability fix: crossing the
// configured threshold emits OnParseGiveUp, surfaces ErrParseGiveUp on
// LoopError, invokes the onUnrecoverable handler, and closes the channels.
func TestDemux_GiveUpTerminates(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	var handlerCalls atomic.Uint64
	var handlerErr atomic.Value
	const threshold uint = 3
	d, s := makeDemuxWithObserver(t,
		WithObserver(obs),
		WithMaxParseErrors(threshold),
		WithUnrecoverableHandler(func(err error) {
			handlerCalls.Add(1)
			handlerErr.Store(err)
		}),
	)
	defer s.close()

	// Feed exactly `threshold` garbage lines to trip give-up.
	for i := uint(0); i < threshold; i++ {
		s.sendToClient(t, "garbage")
	}

	// LoopError receives the terminal error.
	select {
	case err := <-d.LoopError():
		if !errors.Is(err, ErrParseGiveUp) {
			t.Fatalf("LoopError = %v, want ErrParseGiveUp", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for give-up LoopError")
	}

	// (1) give-up telemetry emitted with the threshold count.
	if got := obs.giveUpN.Load(); got != 1 {
		t.Fatalf("OnParseGiveUp call count = %d, want 1", got)
	}
	if got := obs.giveUp.Load(); got != uint64(threshold) {
		t.Fatalf("OnParseGiveUp consecutive = %d, want %d", got, threshold)
	}
	// (2) the unrecoverable handler fired exactly once with the terminal error.
	if got := handlerCalls.Load(); got != 1 {
		t.Fatalf("onUnrecoverable call count = %d, want 1", got)
	}
	if err, _ := handlerErr.Load().(error); !errors.Is(err, ErrParseGiveUp) {
		t.Fatalf("onUnrecoverable err = %v, want ErrParseGiveUp", err)
	}
	// (3) the consecutive malformed-frame run is represented by one gap before
	// the channel closes. Telemetry above retains the exact frame count.
	var gaps uint
	for note := range d.Notifications() {
		if note.DecodeError == nil {
			t.Fatalf("notification before give-up close = %+v, want decode gap", note)
		}
		gaps++
	}
	if gaps != 1 {
		t.Fatalf("decode gaps before close = %d, want 1", gaps)
	}
}

func TestDemux_GiveUpAboveNotificationCapacityDoesNotBlockWithoutConsumer(t *testing.T) {
	t.Parallel()

	const threshold uint = 100
	d, s := makeDemuxWithObserver(t, WithMaxParseErrors(threshold))
	defer s.close()

	writeDone := make(chan error, 1)
	go func() {
		for range threshold {
			if _, err := s.serverOut.Write([]byte("garbage\n")); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()

	select {
	case err := <-d.LoopError():
		if !errors.Is(err, ErrParseGiveUp) {
			t.Fatalf("LoopError = %v, want ErrParseGiveUp", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("demux blocked on gap delivery before reaching parse threshold")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write malformed frames: %v", err)
	}

	var gaps int
	for note := range d.Notifications() {
		if note.DecodeError == nil {
			t.Fatalf("notification before give-up close = %+v, want decode gap", note)
		}
		gaps++
	}
	if gaps != 1 {
		t.Fatalf("decode gaps before close = %d, want 1", gaps)
	}
}

// TestDemux_DefaultGiveUpThreshold confirms the zero-value option keeps the
// package default rather than giving up immediately.
func TestDemux_DefaultGiveUpThreshold(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	d, s := makeDemuxWithObserver(t, WithObserver(obs), WithMaxParseErrors(0))
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// One bad line is well under the default — no give-up.
	s.sendToClient(t, `nope`)
	waitFor(t, func() bool { return obs.parseErrN.Load() >= 1 })
	if obs.giveUpN.Load() != 0 {
		t.Fatal("gave up on a single parse error despite default threshold of 10")
	}
}

// TestDemux_OnUnknownMessage fires on an unclassifiable frame (valid JSON with
// no id, method, or result).
func TestDemux_OnUnknownMessage(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	d, s := makeDemuxWithObserver(t, WithObserver(obs))
	defer func() {
		_ = d.Close()
		s.close()
	}()

	// {"params":{...}} decodes fine but classifies as nothing.
	s.sendToClient(t, `{"params":{"x":1}}`)
	waitFor(t, func() bool { return obs.unknownCount() >= 1 })
	obs.mu.Lock()
	got := append([]string(nil), obs.unknown...)
	obs.mu.Unlock()
	if len(got) != 1 || got[0] != "unclassifiable-frame" {
		t.Fatalf("unknown discriminators = %v, want [unclassifiable-frame]", got)
	}
}

// TestDemux_OnBackpressure fires when the notifications channel saturates. The
// channel cap is 64; we never drain it and push >64 notifications, so the
// read loop must block and emit backpressure at least once.
func TestDemux_OnBackpressure(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{}
	d, s := makeDemuxWithObserver(t, WithObserver(obs))

	// Producer goroutine: write notifications directly to the server-out pipe
	// until the test signals stop. It does NOT call t.Fatal (that would panic
	// when the pipe closes during teardown); a write error just ends the loop.
	stop := make(chan struct{})
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		frame := []byte(`{"method":"turn/started","params":{"threadId":"t1"}}` + "\n")
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.serverOut.Write(frame); err != nil {
				return
			}
		}
	}()

	// Teardown: stop the producer, then close the demux + pipes. Order matters —
	// closing the pipe first would race the producer's Write.
	t.Cleanup(func() {
		close(stop)
		s.close() // unblocks any producer Write parked on a full pipe
		<-producerDone
		_ = d.Close()
	})

	waitFor(t, func() bool { return obs.backpressure.Load() >= 1 })
	if obs.backpressure.Load() == 0 {
		t.Fatal("OnBackpressure never fired despite saturated notifications channel")
	}
}

// --- helpers ---

// waitFor polls cond until true or fails after 3s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("waitFor: condition never became true within 3s")
		case <-time.After(5 * time.Millisecond): // Sleep: bounded poll for async read loop
		}
	}
}

// drainBlocking reads exactly n items from ch, failing if they don't arrive
// within 3s. Returns the items read.
func drainBlocking[T any](t *testing.T, ch <-chan T, n int) []T {
	t.Helper()
	var out []T
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case v, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, v)
		case <-deadline:
			t.Fatalf("drainBlocking: got %d of %d items within 3s", len(out), n)
		}
	}
	return out
}
