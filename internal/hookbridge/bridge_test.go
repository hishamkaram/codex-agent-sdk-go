package hookbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// dialAndRoundTrip simulates the shim's callSDK: dials the listener,
// writes a HookRequest, reads a HookResponse.
func dialAndRoundTrip(t *testing.T, socketPath string, stdinJSON string) HookResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	req := HookRequest{ShimVersion: ShimVersion, Stdin: stdinJSON}
	body, _ := json.Marshal(req)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := conn.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}

	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	n := binary.BigEndian.Uint32(header[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read body: %v", err)
	}
	var resp HookResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestListener_PreToolUseDeny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	var invoked atomic.Int32
	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		invoked.Add(1)
		if in.HookEventName != types.HookPreToolUse {
			t.Errorf("event = %q", in.HookEventName)
		}
		return types.HookDeny{Reason: "not on allowlist"}
	}
	ln, err := New(Config{SocketPath: socket, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	resp := dialAndRoundTrip(t, socket,
		`{"hook_event_name":"preToolUse","tool_input":{"command":"rm -rf /"}}`)

	if invoked.Load() != 1 {
		t.Fatalf("callback invoked %d times", invoked.Load())
	}
	if resp.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", resp.ExitCode)
	}
	if resp.Stderr != "not on allowlist" {
		t.Fatalf("Stderr = %q", resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, `"permissionDecision":"deny"`) {
		t.Fatalf("Stdout missing deny decision: %q", resp.Stdout)
	}
	if !strings.Contains(resp.Stdout, `"permissionDecisionReason":"not on allowlist"`) {
		t.Fatalf("Stdout missing reason: %q", resp.Stdout)
	}
}

func TestListener_PreToolUseAllowIsSilent(t *testing.T) {
	t.Parallel()
	// codex 0.121.0 rejects `permissionDecision:"allow"` and
	// `updatedInput` for PreToolUse. The SDK emits empty stdout with
	// exit 0 — codex defaults to allow. UpdatedInput is silently
	// dropped because codex does not consume it.
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		return types.HookAllow{
			UpdatedInput: json.RawMessage(`{"command":"echo rewritten"}`),
		}
	}
	ln, _ := New(Config{SocketPath: socket, Handler: handler})
	t.Cleanup(func() { _ = ln.Close() })

	resp := dialAndRoundTrip(t, socket,
		`{"hook_event_name":"preToolUse","tool_input":{"command":"ls"}}`)

	if resp.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (allow)", resp.ExitCode)
	}
	if resp.Stdout != "" {
		t.Errorf("PreToolUse allow stdout must be empty (codex rejects allow JSON), got %q", resp.Stdout)
	}
}

func TestListener_PreToolUseAskIsSilent(t *testing.T) {
	t.Parallel()
	// codex 0.121.0 rejects `permissionDecision:"ask"`. The SDK emits
	// empty stdout with exit 0, which lets codex's normal approval flow
	// run when the policy gates the action.
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		return types.HookAsk{Reason: "defer to user"}
	}
	ln, _ := New(Config{SocketPath: socket, Handler: handler})
	t.Cleanup(func() { _ = ln.Close() })

	resp := dialAndRoundTrip(t, socket, `{"hook_event_name":"preToolUse"}`)
	if resp.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", resp.ExitCode)
	}
	if resp.Stdout != "" {
		t.Errorf("PreToolUse ask stdout must be empty (codex rejects ask JSON), got %q", resp.Stdout)
	}
}

func TestListener_CallbackTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		// Sleep past the timeout.
		select {
		case <-time.After(2 * time.Second):
			return types.HookDeny{Reason: "slow"}
		case <-ctx.Done():
			// Return ignored — runHandlerWithRecover short-circuits on ctx done.
			return types.HookDeny{Reason: "ctx canceled"}
		}
	}
	ln, _ := New(Config{
		SocketPath: socket,
		Handler:    handler,
		Timeout:    50 * time.Millisecond,
	})
	t.Cleanup(func() { _ = ln.Close() })

	resp := dialAndRoundTrip(t, socket, `{"hook_event_name":"preToolUse"}`)
	// After timeout the SDK should fail open with HookAllow, exit 0.
	if resp.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (fail-open on timeout)", resp.ExitCode)
	}
}

func TestListener_CallbackPanicsFailsOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	handler := func(ctx context.Context, in types.HookInput) types.HookDecision {
		panic("callback exploded")
	}
	ln, _ := New(Config{SocketPath: socket, Handler: handler})
	t.Cleanup(func() { _ = ln.Close() })

	resp := dialAndRoundTrip(t, socket, `{"hook_event_name":"preToolUse"}`)
	if resp.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (fail-open on panic)", resp.ExitCode)
	}
}

func TestListener_CloseRemovesSocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	ln, _ := New(Config{
		SocketPath: socket,
		Handler:    types.DefaultAllowHookHandler,
	})

	// Socket file should exist pre-close. Use os.Stat (doesn't spawn a
	// serve goroutine that would block Close for the 30s deadline).
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("pre-close stat: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	// Post-close: socket file is gone.
	if _, err := os.Stat(socket); err == nil {
		t.Fatal("socket file still exists after Close")
	}
}

func TestGenerateHooksJSON_Shape(t *testing.T) {
	t.Parallel()
	data, err := GenerateHooksJSON("/bin/codex-sdk-hook-shim", 30)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// All five hook event names present in PascalCase (codex 0.121.0
	// rejects camelCase keys silently).
	for _, name := range []string{"PreToolUse", "PostToolUse", "SessionStart", "UserPromptSubmit", "Stop"} {
		if !strings.Contains(s, `"`+name+`"`) {
			t.Errorf("missing hook event %q in generated config:\n%s", name, s)
		}
	}
	// Old camelCase keys MUST NOT appear — that was the v0.2.0 bug.
	for _, name := range []string{`"preToolUse"`, `"postToolUse"`, `"sessionStart"`, `"userPromptSubmit"`, `"stop"`} {
		if strings.Contains(s, name) {
			t.Errorf("legacy camelCase key %s leaked into generated config:\n%s", name, s)
		}
	}
	// Matcher is `.*` (codex parses it as a regex; `*` alone is not valid).
	if !strings.Contains(s, `"matcher": ".*"`) {
		t.Errorf("missing `.*` matcher:\n%s", s)
	}
	// Command points at shim.
	if !strings.Contains(s, `"command": "/bin/codex-sdk-hook-shim"`) {
		t.Errorf("missing shim path:\n%s", s)
	}
	// Type command.
	if !strings.Contains(s, `"type": "command"`) {
		t.Errorf("missing type=command:\n%s", s)
	}
	// Timeout is in seconds — 30, NOT 30000.
	if !strings.Contains(s, `"timeout": 30`) {
		t.Errorf("missing seconds-unit timeout:\n%s", s)
	}
	if strings.Contains(s, `"timeout": 30000`) {
		t.Errorf("legacy ms-unit timeout leaked (must be seconds):\n%s", s)
	}
}

func TestGenerateHooksJSON_DefaultTimeout(t *testing.T) {
	t.Parallel()
	data, err := GenerateHooksJSON("/bin/codex-sdk-hook-shim", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"timeout": 30`) {
		t.Errorf("default timeout should be 30 seconds:\n%s", data)
	}
}

func TestGenerateHooksJSON_RoundTripsHooksConfig(t *testing.T) {
	t.Parallel()
	data, err := GenerateHooksJSON("/path/to/shim", 45)
	if err != nil {
		t.Fatal(err)
	}
	var cfg HooksConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"PreToolUse", "PostToolUse", "SessionStart", "UserPromptSubmit", "Stop"} {
		groups, ok := cfg.Hooks[name]
		if !ok {
			t.Errorf("missing hook event %q", name)
			continue
		}
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Errorf("event %q: unexpected handler shape: %+v", name, groups)
			continue
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("event %q: type = %q, want command", name, h.Type)
		}
		if h.Command != "/path/to/shim" {
			t.Errorf("event %q: command = %q", name, h.Command)
		}
		if h.Timeout != 45 {
			t.Errorf("event %q: timeout = %d, want 45", name, h.Timeout)
		}
		if groups[0].Matcher != ".*" {
			t.Errorf("event %q: matcher = %q, want .*", name, groups[0].Matcher)
		}
	}
}

func TestGenerateHooksJSON_EmptyShim(t *testing.T) {
	t.Parallel()
	_, err := GenerateHooksJSON("", 30)
	if err == nil {
		t.Fatal("expected error for empty shim path")
	}
}
