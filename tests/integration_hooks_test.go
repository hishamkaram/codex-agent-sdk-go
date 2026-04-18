//go:build integration

// Integration tests for the v0.2.0 hook bridge. These tests require:
//   - real codex CLI on PATH (0.121.0+)
//   - OPENAI_API_KEY or ~/.codex/auth.json
//   - CODEX_SDK_RUN_TURNS=1 (hook tests fire real turns and consume quota)
//
// The tests build the shim binary into a tempdir and point the SDK at it
// via WithShimPath — no global `go install` required.
package tests

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// buildShimBinary compiles cmd/codex-sdk-hook-shim into t.TempDir() and
// returns the absolute path. Skips the test on build error.
func buildShimBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "codex-sdk-hook-shim")
	// Locate the repo root from the test binary's CWD.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(cwd) // tests/ → repo root
	cmd := exec.Command("go", "build", "-o", out, "./cmd/codex-sdk-hook-shim/")
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("failed to build shim (%v): %s", err, stderr.String())
	}
	return out
}

// TestIntegration_HooksEndToEnd is the canonical v0.2.0 hook integration
// test. It covers most of the coverage-matrix rows in a single run:
//
//   - shim binary is built and discovered via WithShimPath (covers
//     WithShimPath + shim-install story)
//   - WithHookCallback auto-enables codex_hooks (covers WithFeatureEnabled)
//   - tempdir CODEX_HOME created on Connect, removed on Close
//     (covers CODEX_HOME lifecycle + socket cleanup)
//   - sessionStart hook observer events arrive (covers HookSessionStart)
//   - turn runs; at least one HookStarted + HookCompleted pair observed
//   - Go callback fires with a recognizable HookEventName
//
// Runs a minimal turn ("Reply with exactly: OK") to trigger the hook
// pipeline. Gated by CODEX_SDK_RUN_TURNS=1 to avoid quota burn in PRs.
func TestIntegration_HooksEndToEnd(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run real turns")
	}
	// v0.2.0: the full auto-wiring path (SDK writes hooks.json into
	// CODEX_HOME override + shim setup) is deferred to v0.3.0. Upstream
	// codex requires CODEX_HOME to be outside /tmp and rejects
	// tempdir-generated hooks.json in ways that block turn startup. The
	// listener IS started, so callers who manually configure
	// ~/.codex/hooks.json to point at codex-sdk-hook-shim will still
	// have callbacks fire — but the SDK can't automate that yet.
	t.Skip("v0.2.0: hook auto-wiring deferred to v0.3.0; see docs/hooks.md for the DIY path")

	shim := buildShimBinary(t)

	var (
		callbackCount atomic.Int32
		seenEvents    = make(map[types.HookEventName]int32)
	)
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		callbackCount.Add(1)
		if cnt, ok := seenEvents[in.HookEventName]; ok {
			atomic.AddInt32(&cnt, 1)
		}
		return types.HookAllow{}
	}

	opts := types.NewCodexOptions().
		WithShimPath(shim).
		WithHookCallback(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Capture the CODEX_HOME tempdir BEFORE Close removes it.
	// (We don't have a public accessor; inspect os.Environ via strings
	// is fragile. Best-effort: find a codex-sdk-home-* entry in the
	// user's TMPDIR before close.)
	tmpBase := os.TempDir()
	preCloseHomes := listSDKHomes(tmpBase)
	if len(preCloseHomes) == 0 {
		t.Errorf("expected a codex-sdk-home-* tempdir before Close; found none under %s", tmpBase)
	}

	t.Cleanup(func() {
		_ = client.Close(context.Background())
		// Post-close assertions: every tempdir the SDK created earlier
		// should be gone.
		postCloseHomes := listSDKHomes(tmpBase)
		for _, p := range preCloseHomes {
			for _, q := range postCloseHomes {
				if p == q {
					t.Errorf("Close did not remove tempdir %q", p)
				}
			}
		}
	})

	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalOnRequest,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	events, err := thread.RunStreamed(ctx, "Reply with exactly: OK", nil)
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}

	var (
		hookStartedSeen   bool
		hookCompletedSeen bool
		hookEventNames    = make(map[types.HookEventName]bool)
		sawTurnCompleted  bool
	)
	for ev := range events {
		switch e := ev.(type) {
		case *types.HookStarted:
			hookStartedSeen = true
			hookEventNames[e.Run.EventName] = true
		case *types.HookCompleted:
			hookCompletedSeen = true
			hookEventNames[e.Run.EventName] = true
		case *types.TurnCompleted:
			sawTurnCompleted = true
		}
	}

	if !sawTurnCompleted {
		t.Fatal("no TurnCompleted event — turn never completed cleanly")
	}
	if !hookStartedSeen {
		t.Fatal("no HookStarted event arrived — shim may not have been invoked by codex")
	}
	if !hookCompletedSeen {
		t.Fatal("no HookCompleted event arrived")
	}
	if callbackCount.Load() == 0 {
		t.Fatal("Go HookCallback never fired — the bridge is not end-to-end")
	}
	names := make([]string, 0, len(hookEventNames))
	for n := range hookEventNames {
		names = append(names, string(n))
	}
	t.Logf("end-to-end: callback fires=%d, observer hook events for %v",
		callbackCount.Load(), names)
}

// TestIntegration_HookDeny confirms HookDeny blocks codex's tool run.
func TestIntegration_HookDeny(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run real turns")
	}
	t.Skip("v0.2.0: hook auto-wiring deferred to v0.3.0; see docs/hooks.md")

	shim := buildShimBinary(t)

	var denyCount atomic.Int32
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		if in.HookEventName == types.HookPreToolUse {
			denyCount.Add(1)
			return types.HookDeny{Reason: "blocked by integration test"}
		}
		return types.HookAllow{}
	}

	opts := types.NewCodexOptions().
		WithShimPath(shim).
		WithHookCallback(handler).
		WithSandbox(types.SandboxWorkspaceWrite).
		WithApprovalPolicy(types.ApprovalUntrusted)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	thread, err := client.StartThread(ctx, nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	turn, err := thread.Run(ctx,
		"Run `ls` in the current working directory and show me the output.", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("turn completed: status=%q items=%d callback-denies=%d",
		turn.Status, len(turn.Items), denyCount.Load())

	if denyCount.Load() == 0 {
		t.Fatal("HookDeny was never invoked — codex may not have fired a preToolUse hook")
	}
	// Inspect turn.Items for a CommandExecution — its status should
	// reflect the block (either 'denied' or a visible error).
	foundCommand := false
	for _, it := range turn.Items {
		if cmd, ok := it.(*types.CommandExecution); ok {
			foundCommand = true
			t.Logf("command %q finished status=%q aggregatedOutput=%q",
				cmd.Command, cmd.Status, truncate(cmd.AggregatedOutput))
		}
	}
	_ = foundCommand // log-only — model may not even try to run a command if hook denied early
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}

// listSDKHomes returns absolute paths of codex-sdk-home-* tempdirs under
// the given base directory.
func listSDKHomes(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "codex-sdk-home-") {
			out = append(out, filepath.Join(base, e.Name()))
		}
	}
	return out
}

// TestIntegration_HookBridgeListenerLifecycle verifies that registering
// a HookCallback starts a Unix socket listener under ~/.cache/codex-sdk/
// on Connect and removes it on Close. v0.2.0 ships this listener wiring
// but does NOT auto-generate hooks.json / override CODEX_HOME — that's
// v0.3.0 work. Users who want hooks to actually fire today must write
// their own hooks.json pointing at codex-sdk-hook-shim and ensure
// CODEX_SDK_HOOK_SOCKET reaches the shim.
func TestIntegration_HookBridgeListenerLifecycle(t *testing.T) {
	requireCodex(t)
	requireAuth(t)

	shim := buildShimBinary(t)
	opts := types.NewCodexOptions().
		WithShimPath(shim).
		WithHookCallback(types.DefaultAllowHookHandler)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	home, _ := os.UserHomeDir()
	sockPath := filepath.Join(home, ".cache", "codex-sdk", fmt.Sprintf("hook-%d.sock", os.Getpid()))
	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("expected socket at %s, stat: %v", sockPath, err)
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sockPath); err == nil {
		t.Errorf("socket at %s still exists after Close", sockPath)
	}
	t.Logf("listener lifecycle ok: created + removed %s", sockPath)
}
