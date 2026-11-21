//go:build integration

package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestIntCmd_ThreadSetName_Happy(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	newName := "_v040_probe_renamed_" + nowSuffix()
	if err := thread.SetName(context.Background(), newName); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	t.Logf("renamed thread %q to %q", thread.ID(), newName)
}

func TestIntCmd_ThreadSetName_EmptyName(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.SetName(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestIntCmd_ThreadRollback_NumTurnsZero(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.Rollback(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error for numTurns=0")
	}
	if !strings.Contains(err.Error(), "numTurns must be >= 1") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_ThreadRollback_LargerThanHistory(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	// On an empty thread, n=1 likely returns an RPC error.
	err := thread.Rollback(context.Background(), 1)
	if err == nil {
		t.Logf("Rollback on empty thread succeeded (codex 0.121.0 may no-op)")
		return
	}
	t.Logf("Rollback on empty thread errored as expected: %v", err)
}

// ====================================================================
// Thread.CleanBackgroundTerminals (requires experimentalApi)
// ====================================================================

func TestIntCmd_CleanBackgroundTerminals_FeatureNotEnabled(t *testing.T) {
	// Default Client (experimentalApi: false) → expect typed error.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.CleanBackgroundTerminals(context.Background())
	if err == nil {
		t.Fatal("expected FeatureNotEnabledError when experimentalApi is off")
	}
	if !types.IsFeatureNotEnabledError(err) {
		t.Errorf("expected FeatureNotEnabledError, got %T: %v", err, err)
	}
}

func TestIntCmd_CleanBackgroundTerminals_HappyWithCapability(t *testing.T) {
	requireAuth(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := integrationOptions(t).WithExperimentalAPI(true)
	c, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	thread, err := c.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	t.Cleanup(func() {
		archCtx, archCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archCancel()
		_ = c.ArchiveThread(archCtx, thread.ID())
	})
	if err := thread.CleanBackgroundTerminals(context.Background()); err != nil {
		t.Fatalf("CleanBackgroundTerminals (with experimentalApi): %v", err)
	}
}

// ====================================================================
// Thread.Steer (needs active turn — quota-gated)
// ====================================================================

func TestIntCmd_ThreadSteer_NoActiveTurn(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	err := thread.Steer(context.Background(), "extend the plan")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no active turn") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_ThreadSteer_ActiveRunStreamed(t *testing.T) {
	requireRunTurns(t)
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	events, err := thread.RunStreamed(ctx, "Use the shell tool to run: sleep 2; echo ready. Then answer with the output.", nil)
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}

	steered := false
	steer := func() {
		if steered {
			return
		}
		err := thread.Steer(ctx, "Also include the word STEERED in the final answer.")
		if err == nil {
			steered = true
			return
		}
		if strings.Contains(err.Error(), "no active turn") {
			return
		}
		t.Fatalf("Steer: %v", err)
	}

	steer()
	for ev := range events {
		if _, ok := ev.(*types.TurnStarted); ok {
			steer()
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("RunStreamed context ended before turn completion: %v", ctx.Err())
	}
	if !steered {
		t.Fatal("never observed an active turn to steer")
	}
}

// ====================================================================
// Thread.Compact — async with pre-installed subscription
// ====================================================================

func TestIntCmd_Compact_EmptyThreadAckSucceeds(t *testing.T) {
	// Verified live: thread/compact/start returns `{}` ack even on
	// an empty thread (no completed turns). The async notification
	// may never arrive for an empty thread, but the RPC itself
	// doesn't error. Our test asserts the sync contract.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	result, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result == nil {
		t.Fatal("nil CompactResult")
	}
	if result.ThreadID != thread.ID() {
		t.Errorf("ThreadID = %q, want %q", result.ThreadID, thread.ID())
	}
	// Detach the subscription so the throwaway-cleanup doesn't leak
	// the goroutine waiting on it.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = result.Wait(ctx)
	if err == nil {
		t.Log("note: unexpectedly received a ContextCompacted event on empty thread")
	} else if !strings.Contains(err.Error(), "context") {
		t.Errorf("Wait err = %q, want ctx cancel", err)
	}
}

func TestIntCmd_Compact_InFlightSecondCallRejected(t *testing.T) {
	// The second Compact must be rejected whether the provider has sent
	// completion for the first operation or it still needs confirmation.
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	result1, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = result1.Wait(ctx) // drain + detach
	})

	result2, err := thread.Compact(context.Background(), nil)
	if result2 != nil || (!errors.Is(err, types.ErrCompactResultPending) &&
		!errors.Is(err, codex.ErrThreadExecutionUnconfirmed)) {
		t.Fatalf("second Compact = (%v, %v), want a pending or unconfirmed operation error", result2, err)
	}
}

func TestIntCmd_CompactResult_WaitCtxCancelAndReEnter(t *testing.T) {
	// Wait with a short ctx → detach subscription → a SECOND
	// Compact on the same thread should succeed (no leaked sub).
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	r1, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, _ = r1.Wait(ctx)
	cancel()

	// The completed wait releases the subscription for a second compaction.
	r2, err := thread.Compact(context.Background(), nil)
	if err != nil {
		t.Fatalf("second Compact after Wait ctx-cancel: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = r2.Wait(ctx)
	})
}

// ====================================================================
// Thread.Summarize — sugar for Compact
// ====================================================================

func TestIntCmd_Summarize_IsAliasForCompact(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	r, err := thread.Summarize(context.Background())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if r == nil {
		t.Fatal("nil result")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = r.Wait(ctx)
	})
}

// ====================================================================
// Thread.StartReview — sync ack, events stream later
// ====================================================================

func TestIntCmd_StartReview_TargetRequired(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	_, err := thread.StartReview(context.Background(), types.ReviewOptions{})
	if err == nil {
		t.Fatal("expected error on empty target")
	}
	if !strings.Contains(err.Error(), "opts.Target.Type is required") {
		t.Errorf("err = %q", err)
	}
}

func TestIntCmd_StartReview_UncommittedChanges_Detached(t *testing.T) {
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run review (consumes quota)")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cwd := schemaWorkspace(t)
	gitCommit := exec.CommandContext(ctx, "git", "-C", cwd, "-c", "user.name=SDK Integration",
		"-c", "user.email=sdk@example.invalid", "-c", "commit.gpgsign=false", "commit",
		"--allow-empty", "--quiet", "-m", "Seed review workspace")
	if out, err := gitCommit.CombinedOutput(); err != nil {
		t.Fatalf("seed review workspace: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(cwd, "review-fixture.txt"), []byte("SDK review fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := connectReadOnlyClient(t)
	thread, err := c.StartThread(ctx, &types.ThreadOptions{
		Cwd: cwd, Sandbox: types.SandboxReadOnly, ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatal(err)
	}
	archive := func(id string) {
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if err := c.ArchiveThread(cleanup, id); err != nil {
				t.Logf("archive review fixture thread: %v", err)
			}
		})
	}
	archive(thread.ID())

	// Materialize the source history that both native and SDK-forked
	// detached reviews inherit.
	seed, err := thread.Run(ctx, "Reply with exactly: OK. Do not use tools.", nil)
	if err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	if seed.Status != "completed" || seed.ID == "" {
		t.Fatalf("seed turn did not complete: status=%s", seed.Status)
	}
	events, err := c.SubscribeThreadEvents(ctx, 512)
	if err != nil {
		t.Fatal(err)
	}

	result, err := thread.StartReview(ctx, types.ReviewOptions{
		Target:   types.ReviewTargetUncommittedChanges(),
		Delivery: types.ReviewDetached,
	})
	if err != nil {
		t.Fatalf("StartReview: %v", err)
	}
	if result == nil || result.ReviewThreadID == "" || result.ReviewThreadID == thread.ID() || result.Turn.ID == "" {
		t.Fatalf("review did not acknowledge a turn on a distinct thread: %+v", result)
	}
	archive(result.ReviewThreadID)
	waitForDetachedReview(t, ctx, events, result.ReviewThreadID, result.Turn.ID)

	source, err := c.ReadThread(ctx, thread.ID(), &types.ThreadReadOptions{IncludeTurns: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Thread.Turns) != 1 || source.Thread.Turns[0].ID != seed.ID {
		t.Fatal("detached review changed the source's persisted turns")
	}
	t.Log("detached review completed on its own thread and preserved source history")
}

func waitForDetachedReview(t *testing.T, ctx context.Context, events <-chan codex.ThreadEventEnvelope, threadID, turnID string) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("wait for detached review: %v", ctx.Err())
		case event, open := <-events:
			if !open || event.Err != nil {
				t.Fatalf("detached review event stream ended: %v", event.Err)
			}
			if event.ThreadID != threadID {
				continue
			}
			switch terminal := event.Event.(type) {
			case *types.TurnCompleted:
				if terminal.TurnID != turnID {
					continue
				}
				if terminal.Status != "completed" {
					t.Fatalf("detached review ended with status %q: %s", terminal.Status, terminal.Error)
				}
				return
			case *types.TurnFailed:
				if terminal.TurnID == turnID {
					t.Fatalf("detached review failed: %s", terminal.Message)
				}
			}
		}
	}
}

func TestIntCmd_StartReview_CommitTarget(t *testing.T) {
	c := connectReadOnlyClient(t)
	thread := newThrowawayThread(t, c)
	// Use a clearly-invalid SHA — expect an error response (not a
	// connection bug).
	opts := types.ReviewOptions{
		Target: types.ReviewTargetCommit("0000000000000000000000000000000000000000", ""),
	}
	_, err := thread.StartReview(context.Background(), opts)
	if err == nil {
		t.Log("note: codex accepted the invalid SHA (may error later via events)")
		return
	}
	t.Logf("invalid SHA rejected: %v", err)
}

// ====================================================================
// GitDiffToRemote (wire method)
// ====================================================================
