package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCallSDK_RoundTrip spins up an in-process Unix socket server that
// plays the SDK role, verifies the shim sends a HookRequest and gets back
// a HookResponse intact.
func TestCallSDK_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	receivedCh := make(chan HookRequest, 1)
	serverDone := &sync.WaitGroup{}
	serverDone.Add(1)
	go func() {
		defer serverDone.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Read one frame.
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			t.Errorf("server read header: %v", err)
			return
		}
		n := binary.BigEndian.Uint32(header[:])
		body := make([]byte, n)
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("server read body: %v", err)
			return
		}
		var req HookRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}
		receivedCh <- req
		// Respond with a deny.
		resp := HookResponse{
			Stdout:   `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"test"}}`,
			Stderr:   "test deny\n",
			ExitCode: 2,
		}
		respBytes, _ := json.Marshal(resp)
		binary.BigEndian.PutUint32(header[:], uint32(len(respBytes)))
		_, _ = conn.Write(header[:])
		_, _ = conn.Write(respBytes)
	}()

	stdin := []byte(`{"hook_event_name":"preToolUse","tool_input":{"command":"rm -rf /"}}`)
	resp, err := callSDK(sock, stdin)
	if err != nil {
		t.Fatalf("callSDK: %v", err)
	}
	serverDone.Wait()

	select {
	case got := <-receivedCh:
		if got.ShimVersion != ShimVersion {
			t.Fatalf("ShimVersion = %q, want %q", got.ShimVersion, ShimVersion)
		}
		if got.Stdin != string(stdin) {
			t.Fatalf("Stdin payload mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received request within 2s")
	}

	if resp.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", resp.ExitCode)
	}
	if resp.Stderr != "test deny\n" {
		t.Fatalf("Stderr = %q", resp.Stderr)
	}
	if !bytes.Contains([]byte(resp.Stdout), []byte(`"permissionDecision":"deny"`)) {
		t.Fatalf("Stdout missing deny decision: %q", resp.Stdout)
	}
}

// TestCallSDK_NoSocket verifies that when the socket path doesn't exist,
// callSDK returns an error (main() then ignores it and exits 0 — tested
// separately).
func TestCallSDK_NoSocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nonexistent := filepath.Join(dir, "missing.sock")
	_, err := callSDK(nonexistent, []byte("{}"))
	if err == nil {
		t.Fatal("expected dial error for nonexistent socket")
	}
}

// TestEnvSubset verifies env-var forwarding behavior.
func TestEnvSubset(t *testing.T) {
	// NOTE: t.Setenv forbids t.Parallel — must run sequentially.
	t.Setenv("CODEX_SDK_HOOK_REQUEST_ID", "abc-123")
	t.Setenv("UNRELATED_ENV", "should-not-leak")

	got := envSubset("CODEX_SDK_HOOK_REQUEST_ID", "NONEXISTENT_KEY")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got["CODEX_SDK_HOOK_REQUEST_ID"] != "abc-123" {
		t.Fatalf("value = %q", got["CODEX_SDK_HOOK_REQUEST_ID"])
	}
	if _, leaked := got["UNRELATED_ENV"]; leaked {
		t.Fatal("UNRELATED_ENV leaked into subset")
	}
}

func TestEnvSubset_NoKeysSet(t *testing.T) {
	// NOTE: mutates os.Unsetenv — sequential.
	os.Unsetenv("DOES_NOT_EXIST_1")
	os.Unsetenv("DOES_NOT_EXIST_2")
	got := envSubset("DOES_NOT_EXIST_1", "DOES_NOT_EXIST_2")
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// TestWriteReadFrame_RoundTrip exercises the low-level framing.
func TestWriteReadFrame_RoundTrip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	payload := []byte(`{"hello":"world"}`)
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip: got %q, want %q", got, payload)
	}
}

func TestReadFrame_TooLarge(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	// Write a header claiming 20 MiB (above 16 MiB cap).
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 20*1024*1024)
	buf.Write(header[:])
	_, err := readFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}
