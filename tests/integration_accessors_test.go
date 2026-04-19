//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// TestIntegration_ClientAccessors exercises Client.ProcessID() and
// Client.SessionID() against a live subprocess + thread lifecycle.
// Pre-Connect values must be zero/empty; post-Connect ProcessID must be
// a real PID; post-StartThread SessionID must equal the thread's ID.
// No billing — does not run a turn.
func TestIntegration_ClientAccessors(t *testing.T) {
	requireCodex(t)
	requireAuth(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := codex.NewClient(ctx, types.NewCodexOptions())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if pid := client.ProcessID(); pid != 0 {
		t.Fatalf("ProcessID before Connect = %d, want 0", pid)
	}
	if sid := client.SessionID(); sid != "" {
		t.Fatalf("SessionID before Connect = %q, want empty", sid)
	}

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	pid := client.ProcessID()
	if pid <= 0 {
		t.Fatalf("ProcessID after Connect = %d, want >0", pid)
	}
	t.Logf("codex app-server pid=%d", pid)

	if sid := client.SessionID(); sid != "" {
		t.Fatalf("SessionID before StartThread = %q, want empty", sid)
	}

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

	if sid := client.SessionID(); sid != thread.ID() {
		t.Fatalf("SessionID after StartThread = %q, want %q", sid, thread.ID())
	}

	// Start a second thread — SessionID must track the newer one.
	second, err := client.StartThread(ctx, &types.ThreadOptions{
		Sandbox:        types.SandboxReadOnly,
		ApprovalPolicy: types.ApprovalOnRequest,
	})
	if err != nil {
		t.Fatalf("StartThread (second): %v", err)
	}
	if sid := client.SessionID(); sid != second.ID() {
		t.Fatalf("SessionID after second StartThread = %q, want %q", sid, second.ID())
	}
}
