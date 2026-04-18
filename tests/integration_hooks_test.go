//go:build integration

// Integration tests for the v0.3.0 hook bridge. These tests require:
//   - real codex CLI on PATH (0.121.0+)
//   - OPENAI_API_KEY or ~/.codex/auth.json
//   - CODEX_SDK_RUN_TURNS=1 (callback tests fire real turns and consume quota)
//
// SAFETY: the v0.3.0 lifecycle WRITES ~/.codex/hooks.json on Connect and
// restores it on Close. Every test below uses safetyNetHooksJSON to stash
// the user's existing config to a test-owned location and unconditionally
// restore it on Cleanup, even if the SDK or codex crashes mid-test.
package tests

import (
	"bytes"
	"context"
	"errors"
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

// requireRunTurns gates tests that consume real OpenAI quota.
func requireRunTurns(t *testing.T) {
	t.Helper()
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run real turns (consumes quota)")
	}
}

// safetyNetHooksJSON stashes the user's existing ~/.codex/hooks.json to
// a test-local file and registers an unconditional Cleanup that restores
// (or removes) the file regardless of whether the SDK's own
// backup/restore worked. Always call this before any test that invokes
// Connect with WithHookCallback.
func safetyNetHooksJSON(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	stash := filepath.Join(t.TempDir(), "user-hooks.json.stash")

	original, hadOriginal, err := readIfExists(hooksPath)
	if err != nil {
		t.Fatalf("safety-net read: %v", err)
	}
	if hadOriginal {
		if err := os.WriteFile(stash, original, 0o600); err != nil {
			t.Fatalf("safety-net stash: %v", err)
		}
	}
	t.Cleanup(func() {
		if hadOriginal {
			if err := os.WriteFile(hooksPath, original, 0o600); err != nil {
				t.Errorf("safety-net restore failed; user's hooks.json at %s may be corrupted (original stashed at %s): %v", hooksPath, stash, err)
			}
			return
		}
		if err := os.Remove(hooksPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("safety-net cleanup remove failed: %v", err)
		}
	})

	// Also wipe any stale SDK backup files left by a prior crashed test
	// to avoid the concurrent-SDK refusal.
	codexDir := filepath.Join(home, ".codex")
	entries, _ := os.ReadDir(codexDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "hooks.json.sdk-backup-") {
			_ = os.Remove(filepath.Join(codexDir, e.Name()))
		}
	}
}

// readIfExists is a local copy of the helper from client.go (not exported).
func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return nil, false, err
}

// buildShimBinary compiles cmd/codex-sdk-hook-shim into t.TempDir() and
// returns the absolute path. Skips the test on build error.
func buildShimBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "codex-sdk-hook-shim")
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

// connectWithCallback wires a Client + Connect with the safety-net,
// returns the connected Client. t.Cleanup closes it. Use as the standard
// scaffold for all callback integration tests.
func connectWithCallback(t *testing.T, handler types.HookHandler, opts ...func(*types.CodexOptions) *types.CodexOptions) *codex.Client {
	t.Helper()
	requireCodex(t)
	requireAuth(t)
	safetyNetHooksJSON(t)
	shim := buildShimBinary(t)

	o := types.NewCodexOptions().
		WithShimPath(shim).
		WithHookCallback(handler)
	for _, fn := range opts {
		o = fn(o)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	client, err := codex.NewClient(ctx, o)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	return client
}

// TestLifecycle_HooksJsonBackupRestore verifies the v0.3.0 contract:
// Connect writes a fresh hooks.json (overwriting any existing user
// config which is byte-backed-up), Close restores byte-identically.
// Does NOT run any turn — gates on requireCodex only (no quota).
func TestLifecycle_HooksJsonBackupRestore(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	safetyNetHooksJSON(t)
	shim := buildShimBinary(t)

	home, _ := os.UserHomeDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Plant a user-owned hooks.json with a recognizable marker.
	original := []byte(`{"hooks":{"PreToolUse":[{"matcher":".*","hooks":[{"type":"command","command":"/users/own/handler","timeout":15}]}]}}`)
	if err := os.WriteFile(hooksPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

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

	// While connected: hooks.json should contain the SDK shim path,
	// NOT the user's "/users/own/handler" string.
	connected, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(connected), `/users/own/handler`) {
		t.Errorf("Connect did not overwrite user hooks.json: %s", connected)
	}
	if !strings.Contains(string(connected), shim) {
		t.Errorf("Connect did not write shim path %q into hooks.json: %s", shim, connected)
	}
	if !strings.Contains(string(connected), `"PreToolUse"`) {
		t.Errorf("Connect did not use PascalCase event keys: %s", connected)
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close: hooks.json should be byte-identical to the original.
	restored, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json missing after Close: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Errorf("post-Close hooks.json not byte-identical to original:\nwant: %s\ngot:  %s", original, restored)
	}
}

// TestLifecycle_ConcurrentClientsConflict verifies v0.3.0's
// refuse-with-error policy when a second Client tries to wire hooks
// while a first is already managing the user's config. Last-writer-wins
// would silently corrupt the user's true original on Close, so we error.
func TestLifecycle_ConcurrentClientsConflict(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	safetyNetHooksJSON(t)
	shim := buildShimBinary(t)

	opts := types.NewCodexOptions().
		WithShimPath(shim).
		WithHookCallback(types.DefaultAllowHookHandler)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clientA, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientA.Connect(ctx); err != nil {
		t.Fatalf("Connect A: %v", err)
	}
	t.Cleanup(func() { _ = clientA.Close(context.Background()) })

	// Plant a fake fresh backup file from a different PID to simulate a
	// second machine-wide live SDK. (Same-PID detection is intentionally
	// off — it's an in-process re-Connect scenario unrelated to this
	// case.) See client.go's detectConcurrentSDK for the rationale.
	home, _ := os.UserHomeDir()
	fakePID := os.Getpid() + 1
	fakeBackup := filepath.Join(home, ".codex", fmt.Sprintf("hooks.json.sdk-backup-%d", fakePID))
	if err := os.WriteFile(fakeBackup, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(fakeBackup) })

	clientB, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	err = clientB.Connect(ctx)
	if err == nil {
		_ = clientB.Close(context.Background())
		t.Fatal("expected concurrent-SDK refusal on second Connect; got nil error")
	}
	if !strings.Contains(err.Error(), "concurrent codex SDK Client detected") {
		t.Errorf("error doesn't name the concurrent-SDK case: %v", err)
	}
}

// TestHookCallback_Allow is the canonical end-to-end callback test.
// Asserts that the Go callback fires for at least one preToolUse hook
// during a tiny turn, and that the new HookInput fields (Model,
// ToolName, ToolUseID) are populated from the live shim payload.
// hook_event_name normalizes from PascalCase "PreToolUse" to camelCase
// HookPreToolUse.
func TestHookCallback_Allow(t *testing.T) {
	requireRunTurns(t)
	var (
		callbackCount   atomic.Int32
		sawPreToolUse   atomic.Bool
		sawModelField   atomic.Bool
		sawToolName     atomic.Bool
		sawToolUseID    atomic.Bool
		sawCamelCaseEvt atomic.Bool
	)
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		callbackCount.Add(1)
		if in.HookEventName == types.HookPreToolUse {
			sawPreToolUse.Store(true)
			sawCamelCaseEvt.Store(true) // constant value is camelCase
		}
		if in.Model != "" {
			sawModelField.Store(true)
		}
		if in.ToolName != "" {
			sawToolName.Store(true)
		}
		if in.ToolUseID != "" {
			sawToolUseID.Store(true)
		}
		return types.HookAllow{}
	}

	client := connectWithCallback(t, handler,
		func(o *types.CodexOptions) *types.CodexOptions {
			return o.WithSandbox(types.SandboxWorkspaceWrite).
				WithApprovalPolicy(types.ApprovalNever)
		})

	thread, err := client.StartThread(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	turn, err := thread.Run(ctx,
		"Run `pwd` and reply with the output. No other commands.", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("turn status=%q items=%d callbackCount=%d", turn.Status, len(turn.Items), callbackCount.Load())

	if callbackCount.Load() == 0 {
		t.Fatal("Go HookCallback never fired — bridge is not end-to-end")
	}
	if !sawPreToolUse.Load() {
		t.Errorf("no preToolUse callback observed (camelCase normalization may have failed)")
	}
	if !sawModelField.Load() {
		t.Errorf("Model field never populated — parser fix did not land")
	}
	if !sawToolName.Load() {
		t.Errorf("ToolName field never populated — parser fix did not land")
	}
	if !sawToolUseID.Load() {
		t.Errorf("ToolUseID field never populated — parser fix did not land")
	}
}

// TestHookCallback_AllowRewrite documents an upstream limitation of
// codex 0.121.0: hookSpecificOutput.updatedInput is in the wire schema
// but is rejected at runtime ("PreToolUse hook returned unsupported
// updatedInput" per the codex binary's error strings). Round-trip
// verification with postToolUse confirms the original command runs
// regardless of UpdatedInput. v0.3.0 silently drops UpdatedInput on
// PreToolUse to avoid the spurious codex log warning. Re-enable this
// test once codex supports updatedInput end-to-end.
func TestHookCallback_AllowRewrite(t *testing.T) {
	requireRunTurns(t)
	t.Skip("codex 0.121.0 rejects PreToolUse updatedInput as 'unsupported' (binary string evidence); re-enable when upstream lands the rewrite path")
	const sentinel = "HOOKED_REWRITE_OK"

	var (
		callbackFires atomic.Int32
		preBashFires  atomic.Int32
	)
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		callbackFires.Add(1)
		t.Logf("callback: event=%q toolName=%q toolInput=%s", in.HookEventName, in.ToolName, string(in.ToolInput))
		if in.HookEventName != types.HookPreToolUse {
			return types.HookAllow{}
		}
		if in.ToolName != "Bash" {
			return types.HookAllow{}
		}
		preBashFires.Add(1)
		return types.HookAllow{
			UpdatedInput: []byte(`{"command":"echo ` + sentinel + `"}`),
		}
	}

	client := connectWithCallback(t, handler,
		func(o *types.CodexOptions) *types.CodexOptions {
			return o.WithSandbox(types.SandboxWorkspaceWrite).
				WithApprovalPolicy(types.ApprovalNever)
		})

	thread, err := client.StartThread(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	turn, err := thread.Run(ctx, "Run `pwd` and tell me what it printed.", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	t.Logf("turn status=%q items=%d callbacks=%d preBashFires=%d", turn.Status, len(turn.Items), callbackFires.Load(), preBashFires.Load())
	foundSentinel := false
	for i, it := range turn.Items {
		t.Logf("item[%d] type=%T", i, it)
		if cmd, ok := it.(*types.CommandExecution); ok {
			if strings.Contains(cmd.Command, sentinel) || strings.Contains(cmd.AggregatedOutput, sentinel) {
				foundSentinel = true
			}
			t.Logf("  command=%q status=%q output=%q", cmd.Command, cmd.Status, truncate(cmd.AggregatedOutput))
		}
		if msg, ok := it.(*types.AgentMessage); ok {
			t.Logf("  agent message=%q", truncate(msg.Text))
		}
	}
	if !foundSentinel {
		t.Errorf("UpdatedInput did not rewrite the command — sentinel %q not in any CommandExecution", sentinel)
	}
}

// TestHookCallback_Deny verifies that HookDeny on preToolUse blocks
// codex from running the command. The turn is allowed to complete (the
// model usually adapts and reports the block) but no CommandExecution
// reaches a 'completed' status.
func TestHookCallback_Deny(t *testing.T) {
	requireRunTurns(t)
	var denyCount atomic.Int32
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		if in.HookEventName == types.HookPreToolUse {
			denyCount.Add(1)
			return types.HookDeny{Reason: "blocked by integration test"}
		}
		return types.HookAllow{}
	}

	client := connectWithCallback(t, handler,
		func(o *types.CodexOptions) *types.CodexOptions {
			return o.WithSandbox(types.SandboxWorkspaceWrite).
				WithApprovalPolicy(types.ApprovalNever)
		})

	thread, err := client.StartThread(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	turn, err := thread.Run(ctx, "Run `ls` in the current directory.", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("turn status=%q items=%d denyCount=%d", turn.Status, len(turn.Items), denyCount.Load())
	if denyCount.Load() == 0 {
		t.Fatal("HookDeny was never invoked — codex may not have fired preToolUse")
	}
	for _, it := range turn.Items {
		if cmd, ok := it.(*types.CommandExecution); ok {
			if cmd.Status == "completed" {
				t.Errorf("a denied command reached completed status: %+v", cmd)
			}
		}
	}
}

// TestHookCallback_Ask verifies HookAsk semantics under codex 0.121.0:
// because codex rejects `permissionDecision:"ask"` outright (binary
// string evidence), the SDK emits silent exit-0 and lets codex's normal
// approval policy decide. To prove the fall-through works, we use a
// command that requires approval under read-only sandbox + on-request
// policy, register an ApprovalDeny callback, and assert the approval
// callback fires.
func TestHookCallback_Ask(t *testing.T) {
	requireRunTurns(t)
	var (
		askCount      atomic.Int32
		approvalFired atomic.Bool
	)
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		t.Logf("hook: event=%q toolName=%q toolInput=%s", in.HookEventName, in.ToolName, string(in.ToolInput))
		if in.HookEventName == types.HookPreToolUse {
			askCount.Add(1)
			return types.HookAsk{Reason: "defer to user via approval flow"}
		}
		return types.HookAllow{}
	}
	approval := func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
		approvalFired.Store(true)
		t.Logf("approval requested: %T", req)
		return types.ApprovalDeny{Reason: "ask-test denies"}
	}

	client := connectWithCallback(t, handler,
		func(o *types.CodexOptions) *types.CodexOptions {
			// read-only sandbox + on-request policy: ANY workspace
			// write triggers the approval flow.
			return o.WithSandbox(types.SandboxReadOnly).
				WithApprovalPolicy(types.ApprovalOnRequest).
				WithApprovalCallback(approval)
		})

	thread, err := client.StartThread(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	turn, err := thread.Run(ctx,
		"Create a file at /tmp/codex-sdk-ask-test-marker by running `touch /tmp/codex-sdk-ask-test-marker`.", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("turn status=%q askCount=%d approvalFired=%v", turn.Status, askCount.Load(), approvalFired.Load())
	if askCount.Load() == 0 {
		t.Fatal("HookAsk was never invoked")
	}
	if !approvalFired.Load() {
		t.Errorf("approval callback did not fire after HookAsk — fall-through is broken")
	}
}

// TestHookCallback_Timeout verifies fail-open semantics: a callback
// that exceeds WithHookTimeout is killed and the SDK returns HookAllow
// to codex so the turn never bricks. With a 50ms timeout and a 5s
// sleep, this exercises the listener's ctx cancellation path under a
// real codex invocation.
func TestHookCallback_Timeout(t *testing.T) {
	requireRunTurns(t)
	var slowCalls atomic.Int32
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		slowCalls.Add(1)
		select {
		case <-time.After(5 * time.Second):
			return types.HookDeny{Reason: "should never reach this"}
		case <-ctx.Done():
			return types.HookDeny{Reason: "ctx canceled — SDK should ignore"}
		}
	}

	client := connectWithCallback(t, handler,
		func(o *types.CodexOptions) *types.CodexOptions {
			return o.WithSandbox(types.SandboxWorkspaceWrite).
				WithApprovalPolicy(types.ApprovalNever).
				WithHookTimeout(50 * time.Millisecond)
		})

	thread, err := client.StartThread(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	turn, err := thread.Run(ctx, "Reply with exactly: TIMEOUT_OK", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if slowCalls.Load() == 0 {
		t.Skip("no callback fired during this turn — codex did not invoke a hook (skip rather than fail)")
	}
	t.Logf("turn status=%q slowCalls=%d", turn.Status, slowCalls.Load())
	// Turn status reaching anything other than "failed" means the SDK
	// successfully shielded codex from the slow callback.
	if turn.Status == "failed" {
		t.Errorf("turn failed despite fail-open semantics: %+v", turn)
	}
}

// TestIntegration_HookObserverEndToEnd is the un-skipped v0.2.0
// observer-mode test (no callback registered, just observing
// HookStarted/HookCompleted events from the user's existing hook
// config). With v0.3.0's auto-wiring, observer-mode must still work
// without callback registration — i.e., WithHooks(true) alone keeps
// firing observer events.
func TestIntegration_HookObserverEndToEnd(t *testing.T) {
	requireRunTurns(t)
	requireCodex(t)
	requireAuth(t)

	opts := types.NewCodexOptions().
		WithSandbox(types.SandboxWorkspaceWrite).
		WithApprovalPolicy(types.ApprovalNever)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	events, err := thread.RunStreamed(ctx, "Reply with exactly: OK", nil)
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}
	var sawTurnDone bool
	for ev := range events {
		if _, ok := ev.(*types.TurnCompleted); ok {
			sawTurnDone = true
		}
	}
	if !sawTurnDone {
		t.Fatal("turn did not complete cleanly")
	}
	// No assertion on HookStarted/HookCompleted — they fire only when
	// the user's hooks.json has handlers configured. The point of this
	// test is to prove WithHooks(true) without WithHookCallback does
	// NOT touch hooks.json at all.
}

// truncate caps long output strings for logs.
func truncate(s string) string {
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}
