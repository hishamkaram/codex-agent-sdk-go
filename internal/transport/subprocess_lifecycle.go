package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// watchExit blocks on cmd.Wait(), records the exit on waitDone for Close(), and
// emits OnSubprocessExit exactly once. Exit code, requested-flag, and cause are
// captured under the lock; the Observer is invoked after the unlock so it never
// runs under the mutex.
func (t *AppServer) watchExit(cmd *exec.Cmd, stderrDone <-chan struct{}) {
	// cmd.Wait() closes the StderrPipe, so per the os/exec docs all reads from that
	// pipe MUST complete before Wait is called — otherwise Wait races the stderr drain
	// and truncates the captured tail mid-copy (issue 79). The process's stderr
	// write-end closes on exit, the drain goroutine reads to EOF and closes stderrDone,
	// and only then is it safe to reap.
	//
	// The wait is BOUNDED by StderrDrainTimeout: on the SIGKILL path a descendant
	// that inherited the stderr write-end can keep the pipe open after the parent is
	// reaped, so stderrDone may never close. We accept a possibly-truncated tail in
	// that rare case rather than hang Close forever. cmd.Wait() reaps only the parent
	// (StderrPipe has no os/exec copy goroutine), so it returns promptly once we stop
	// waiting on the drain.
	if stderrDone != nil {
		select {
		case <-stderrDone:
		case <-time.After(StderrDrainTimeout):
		}
	}
	waitErr := cmd.Wait()
	t.waitDone <- waitErr

	t.mu.Lock()
	t.ready = false
	requested := t.shutdownReq
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	var cause error
	if !requested && waitErr != nil {
		// Unexpected death (the process exited on its own, not via Close). Record
		// it as the transport error (first error wins — a prior give-up error
		// takes precedence).
		if t.lastErr == nil {
			t.lastErr = types.NewProcessError(
				"codex app-server exited unexpectedly: "+waitErr.Error(),
				exitCode,
				t.stderrLocked(),
			)
		}
		cause = t.lastErr
	} else if !requested {
		// Clean self-exit (waitErr == nil) that we did not request.
		cause = t.lastErr
	}
	t.mu.Unlock()

	t.exitOnce.Do(func() {
		t.observer.OnSubprocessExit(exitCode, requested, cause)
	})
}

// terminateOnUnrecoverableError is the demux give-up callback. It records the
// terminal error (first error wins) and kills the subprocess so a parse give-up
// does not leave a zombie. The watchExit goroutine then observes the death and
// emits OnSubprocessExit. Safe to call from the demux read goroutine; Kill is
// idempotent.
func (t *AppServer) terminateOnUnrecoverableError(reason error) {
	t.mu.Lock()
	if t.lastErr == nil {
		t.lastErr = reason
	}
	var proc *os.Process
	if t.cmd != nil {
		proc = t.cmd.Process
	}
	t.mu.Unlock()

	if proc != nil {
		_ = proc.Kill()
	}
}

// Close shuts down the subprocess:
//  1. Close the demux (unblocks in-flight Send calls).
//  2. Close stdin to signal EOF; most agents exit cleanly on EOF.
//  3. Wait up to ShutdownGrace for Wait() to return.
//  4. SIGTERM; wait up to TerminateGrace.
//  5. SIGKILL as last resort.
//
// The ctx parameter is honored — if it's canceled, step 3/4 waits are cut
// short and we escalate faster.
func (t *AppServer) Close(ctx context.Context) error {
	var closeErr error
	t.closedOnce.Do(func() { closeErr = t.doClose(ctx) })
	return closeErr
}

func (t *AppServer) doClose(ctx context.Context) error {
	t.mu.Lock()
	// Mark shutdown requested BEFORE signaling the subprocess so the watchExit
	// goroutine classifies the resulting exit as requested (clean), not as an
	// unexpected death.
	t.shutdownReq = true
	demux := t.demux
	stdin := t.stdinW
	cmd := t.cmd
	waitDone := t.waitDone
	stderrDone := t.stderrDone
	t.mu.Unlock()

	if demux != nil {
		_ = demux.Close()
	}
	if stdin != nil {
		_ = stdin.Close()
	}

	if cmd == nil {
		return nil
	}

	// Stage 1: graceful exit within ShutdownGrace.
	exited, err := waitWithTimeout(ctx, waitDone, ShutdownGrace)
	if exited {
		t.drainStderr(stderrDone)
		return t.classifyExit(err, true)
	}

	// Stage 2: SIGTERM.
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	exited, err = waitWithTimeout(ctx, waitDone, TerminateGrace)
	if exited {
		t.drainStderr(stderrDone)
		return t.classifyExit(err, true)
	}

	// Stage 3: SIGKILL.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	err = <-waitDone
	t.drainStderr(stderrDone)
	return t.classifyExit(err, true)
}

// drainStderr waits for the stderr goroutine to finish so t.stderr is stable
// when callers read it. Call only after cmd.Wait has observed process exit or
// after the process has been killed; at that point the stderr pipe normally
// reaches EOF. The wait is bounded by StderrDrainTimeout so a descendant that
// inherited the stderr write-end cannot wedge Close indefinitely.
func (t *AppServer) drainStderr(done chan struct{}) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(StderrDrainTimeout):
	}
}

// classifyExit maps an *exec.Cmd Wait() error into either nil or a
// *types.ProcessError carrying exit code + stderr tail. Once Close has
// requested shutdown by closing stdin, a non-zero app-server exit is not
// actionable; turn/runtime failures are surfaced through RPC events.
func (t *AppServer) classifyExit(err error, shutdownRequested bool) error {
	if err == nil {
		return nil
	}
	if shutdownRequested {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return types.NewProcessError(
			"codex app-server exited non-zero",
			exitErr.ExitCode(),
			t.Stderr(),
		)
	}
	// Signal or other unexpected termination.
	return types.NewProcessError(
		"codex app-server terminated unexpectedly: "+err.Error(),
		-1,
		t.Stderr(),
	)
}

// waitWithTimeout returns (true, err) when waitDone fires, (false, nil) on
// timeout or context cancel.
func waitWithTimeout(ctx context.Context, waitDone chan error, timeout time.Duration) (bool, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waitDone:
		return true, err
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, nil
	}
}
