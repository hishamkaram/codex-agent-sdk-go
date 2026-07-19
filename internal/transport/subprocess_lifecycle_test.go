package transport

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// TestAppServerWatchExitPublishesStateBeforeWaitDone guards the completion
// ordering used by Close and Client cleanup: receiving waitDone means Health
// has already transitioned out of Ready.
func TestAppServerWatchExitPublishesStateBeforeWaitDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	app := &AppServer{
		observer: types.NopObserver{},
		ready:    true,
		waitDone: make(chan error, 1),
	}
	cmd := exec.Command("codex")
	watchExited := make(chan struct{})

	app.mu.Lock()
	go func(cmd *exec.Cmd) {
		defer close(watchExited)
		app.watchExit(cmd, nil)
	}(cmd)

	// exec.Cmd.Wait returns immediately for an unstarted command. Keep the
	// state lock while the watcher processes that completion. Before the fix,
	// it signaled waitDone before it could acquire this lock and clear Ready.
	timer := time.NewTimer(250 * time.Millisecond)
	earlySignal := false
	readyAtSignal := false
	select {
	case <-app.waitDone:
		earlySignal = true
		readyAtSignal = app.ready
	case <-timer.C:
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	app.mu.Unlock()

	if earlySignal && readyAtSignal {
		t.Fatal("waitDone signaled before watchExit cleared Ready")
	}
	if !earlySignal {
		select {
		case <-app.waitDone:
		case <-ctx.Done():
			t.Fatalf("waitDone was not signaled after state lock release: %v", ctx.Err())
		}
	}
	select {
	case <-watchExited:
	case <-ctx.Done():
		t.Fatalf("watchExit did not return: %v", ctx.Err())
	}
	if h := app.Health(); h.Ready {
		t.Fatalf("Health().Ready = true after waitDone, want false; health=%+v", h)
	}
}
