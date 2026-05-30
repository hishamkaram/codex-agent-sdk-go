package types

import (
	"errors"
	"testing"
	"time"
)

// TestNopObserver_AllMethodsNoOp verifies NopObserver implements Observer and
// every method is a safe no-op (does not panic, returns nothing).
func TestNopObserver_AllMethodsNoOp(t *testing.T) {
	t.Parallel()

	var obs Observer = NopObserver{} // compile-time interface satisfaction
	err := errors.New("boom")

	// None of these should panic.
	obs.OnConnect(5*time.Millisecond, nil)
	obs.OnConnect(5*time.Millisecond, err)
	obs.OnFirstMessage(time.Millisecond)
	obs.OnSubprocessExit(0, true, nil)
	obs.OnSubprocessExit(1, false, err)
	obs.OnParseError(3, err)
	obs.OnParseGiveUp(10)
	obs.OnBackpressure()
	obs.OnUnknownMessage("turn/somethingNew")
}

// TestObserverInterface_SignatureShape pins the exact method set + signatures so
// this interface stays byte-for-byte identical to the claude-agent-sdk-go
// Observer (a single consumer implementation must satisfy both). If a method is
// added/renamed/retyped, this assignment fails to compile.
func TestObserverInterface_SignatureShape(t *testing.T) {
	t.Parallel()

	var _ Observer = interface {
		OnConnect(d time.Duration, err error)
		OnFirstMessage(d time.Duration)
		OnSubprocessExit(exitCode int, requested bool, err error)
		OnParseError(consecutive uint, err error)
		OnParseGiveUp(consecutive uint)
		OnBackpressure()
		OnUnknownMessage(discriminator string)
	}(NopObserver{})
}

// TestTransportHealth_ZeroValue confirms the zero value is the "nothing yet"
// snapshot a consumer reads before Connect.
func TestTransportHealth_ZeroValue(t *testing.T) {
	t.Parallel()

	var h TransportHealth
	if h.Connected || h.Ready || h.PID != 0 || h.LastError != nil {
		t.Fatalf("zero TransportHealth = %+v, want all-zero", h)
	}
}
