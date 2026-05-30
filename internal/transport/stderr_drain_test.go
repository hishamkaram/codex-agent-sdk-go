package transport

import (
	"testing"
	"time"
)

// TestDrainStderrBoundedWhenPipeNeverEOFs verifies drainStderr returns within
// StderrDrainTimeout even when its done channel never closes — the situation
// that arises when a SIGKILLed parent leaves a descendant holding the stderr
// write-end, so io.Copy never reaches EOF. Before the bound this blocked
// forever and hung Close() (and watchExit). The test fails (times out) on the
// pre-fix unbounded `<-done`.
func TestDrainStderrBoundedWhenPipeNeverEOFs(t *testing.T) {
	t.Parallel()

	app := &AppServer{}
	neverCloses := make(chan struct{}) // intentionally never closed

	start := time.Now()
	done := make(chan struct{})
	go func() {
		app.drainStderr(neverCloses)
		close(done)
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed < StderrDrainTimeout/2 {
			t.Fatalf("drainStderr returned after %v, suspiciously fast — expected ~%v bound", elapsed, StderrDrainTimeout)
		}
	case <-time.After(StderrDrainTimeout + 3*time.Second):
		t.Fatal("drainStderr did not return within the bound — StderrDrainTimeout is not honored")
	}
}

// TestDrainStderrReturnsPromptlyWhenDone verifies the fast path: an already-
// closed done channel returns immediately, well under the timeout.
func TestDrainStderrReturnsPromptlyWhenDone(t *testing.T) {
	t.Parallel()

	app := &AppServer{}
	done := make(chan struct{})
	close(done)

	ret := make(chan struct{})
	go func() {
		app.drainStderr(done)
		close(ret)
	}()

	select {
	case <-ret:
	case <-time.After(StderrDrainTimeout):
		t.Fatal("drainStderr blocked despite an already-closed done channel")
	}
}
