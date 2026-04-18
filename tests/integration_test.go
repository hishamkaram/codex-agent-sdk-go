//go:build integration

// Package tests contains integration tests gated by the `integration` build
// tag. They require a real codex CLI on PATH + either OPENAI_API_KEY or a
// populated ~/.codex/auth.json.
//
// Run:
//
//	go test -tags=integration ./tests/...
//
// By default these tests exercise ONLY no-billing flows (initialize, thread
// lifecycle, archive). To additionally run a minimal turn (~5 tokens),
// set CODEX_SDK_RUN_TURNS=1. Turns consume real OpenAI quota.
package tests

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func requireCodex(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("codex CLI not found: %v", err)
	}
	return path
}

func requireAuth(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") != "" {
		return
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(home + "/.codex/auth.json"); err == nil {
		return
	}
	t.Skip("no OPENAI_API_KEY and no ~/.codex/auth.json — cannot auth")
}

// TestIntegration_FindCLIAndVersion exercises FindCLI + ProbeCLIVersion
// against the real binary. No subprocess lifecycle; no billing.
func TestIntegration_FindCLIAndVersion(t *testing.T) {
	path := requireCodex(t)

	found, err := transport.FindCLI()
	if err != nil {
		t.Fatalf("FindCLI: %v", err)
	}
	if found != path {
		t.Logf("FindCLI returned %q, exec.LookPath returned %q (different install locations OK)", found, path)
	}

	v, err := transport.ProbeCLIVersion(found)
	if err != nil {
		t.Fatalf("ProbeCLIVersion: %v", err)
	}
	t.Logf("codex version: %s", v.String())

	// Basic sanity — codex has never been 0.0.x and has no major >10.
	if v.Major > 10 {
		t.Fatalf("implausible major version: %d", v.Major)
	}
	if v.Major == 0 && v.Minor == 0 {
		t.Fatalf("implausible minor version: %+v", v)
	}
}

// TestIntegration_ConnectAndArchive runs the full subprocess lifecycle
// without any turns: initialize → initialized → thread/start →
// thread/archive → close. Verifies our handshake, demux, and shutdown
// ladder survive contact with the real server. No billing.
func TestIntegration_ConnectAndArchive(t *testing.T) {
	requireCodex(t)
	requireAuth(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions().WithVerbose(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	init := client.InitializeResult()
	if init.UserAgent == "" {
		t.Fatal("InitializeResult.UserAgent empty — handshake response shape changed?")
	}
	if !strings.Contains(init.UserAgent, "/") {
		t.Fatalf("UserAgent missing version separator: %q", init.UserAgent)
	}
	t.Logf("connected: user_agent=%q codex_home=%q platform=%s/%s",
		init.UserAgent, init.CodexHome, init.PlatformFamily, init.PlatformOs)

	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalOnRequest,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	if thread.ID() == "" {
		t.Fatal("thread ID empty after StartThread")
	}
	t.Logf("thread: %s", thread.ID())

	if err := client.ArchiveThread(ctx, thread.ID()); err != nil {
		// Some CLI versions don't expose thread/archive as a plain RPC;
		// that's a soft failure — log and continue.
		t.Logf("ArchiveThread soft-failed (may be unsupported): %v", err)
	}
}

// TestIntegration_OneMinimalTurn runs a single ~5-token turn. Consumes
// real quota. Gated by CODEX_SDK_RUN_TURNS=1 so PR CI doesn't burn
// the secret budget.
func TestIntegration_OneMinimalTurn(t *testing.T) {
	requireCodex(t)
	requireAuth(t)
	if os.Getenv("CODEX_SDK_RUN_TURNS") != "1" {
		t.Skip("set CODEX_SDK_RUN_TURNS=1 to run real turns")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalOnRequest,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	turn, err := thread.Run(ctx, "Reply with exactly: OK", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// CLI 0.121.0 reports "completed" on success, "failed" on error.
	// (Earlier design notes used "success" — that was an incorrect guess.)
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed (events=%d items=%d)",
			turn.Status, len(turn.Events), len(turn.Items))
	}
	if turn.FinalResponse == "" {
		t.Fatal("FinalResponse empty — no AgentMessage item arrived")
	}
	if turn.Usage.OutputTokens == 0 {
		t.Fatal("Usage.OutputTokens = 0 — turn/completed missing usage?")
	}
	t.Logf("reply: %q (in=%d out=%d)", turn.FinalResponse,
		turn.Usage.InputTokens, turn.Usage.OutputTokens)
}
