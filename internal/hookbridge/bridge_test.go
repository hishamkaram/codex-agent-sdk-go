package hookbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
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

func TestListener_PreToolUseAllowWithRewrite(t *testing.T) {
	t.Parallel()
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
	if !strings.Contains(resp.Stdout, `"permissionDecision":"allow"`) {
		t.Fatalf("missing allow: %q", resp.Stdout)
	}
	if !strings.Contains(resp.Stdout, `"echo rewritten"`) {
		t.Fatalf("missing rewritten command: %q", resp.Stdout)
	}
}

func TestListener_PreToolUseAsk(t *testing.T) {
	t.Parallel()
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
	if !strings.Contains(resp.Stdout, `"permissionDecision":"ask"`) {
		t.Fatalf("missing ask decision: %q", resp.Stdout)
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

	// Socket should exist.
	if _, err := net.DialTimeout("unix", socket, 500*time.Millisecond); err != nil {
		t.Fatalf("pre-close dial should succeed: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	// File removed.
	if _, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
		t.Fatal("post-close dial should fail")
	}
}

func TestGenerateHooksJSON_Shape(t *testing.T) {
	t.Parallel()
	data, err := GenerateHooksJSON("/bin/codex-sdk-hook-shim", 30_000)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// All five hook event names present.
	for _, name := range []string{"preToolUse", "postToolUse", "sessionStart", "userPromptSubmit", "stop"} {
		if !strings.Contains(s, `"`+name+`"`) {
			t.Errorf("missing hook event %q in generated config:\n%s", name, s)
		}
	}
	// Matcher is wildcard.
	if !strings.Contains(s, `"matcher": "*"`) {
		t.Errorf("missing wildcard matcher:\n%s", s)
	}
	// Command points at shim.
	if !strings.Contains(s, `"command": "/bin/codex-sdk-hook-shim"`) {
		t.Errorf("missing shim path:\n%s", s)
	}
	// Type command.
	if !strings.Contains(s, `"type": "command"`) {
		t.Errorf("missing type=command:\n%s", s)
	}
}

func TestGenerateHooksJSON_EmptyShim(t *testing.T) {
	t.Parallel()
	_, err := GenerateHooksJSON("", 30_000)
	if err == nil {
		t.Fatal("expected error for empty shim path")
	}
}
