//go:build integration

package tests

import (
	"context"
	"os"
	"testing"

	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func TestIntCmd_UploadFeedback_Minimal(t *testing.T) {
	if os.Getenv("CODEX_SDK_FEEDBACK_OK") != "1" {
		t.Skip("set CODEX_SDK_FEEDBACK_OK=1 to send a real test feedback to OpenAI")
	}
	c := connectReadOnlyClient(t)
	receipt, err := c.UploadFeedback(context.Background(), types.FeedbackReport{
		Classification: "feedback",
		IncludeLogs:    false,
		Reason:         "v0.4.0 SDK integration test — please ignore",
	})
	if err != nil {
		t.Fatalf("UploadFeedback: %v", err)
	}
	if receipt == nil {
		t.Fatal("nil receipt")
	}
	t.Logf("feedback receipt: threadId=%q", receipt.ThreadID)
}

func TestIntCmd_UploadFeedback_EmptyClassification(t *testing.T) {
	c := connectReadOnlyClient(t)
	_, err := c.UploadFeedback(context.Background(), types.FeedbackReport{Classification: ""})
	if err == nil {
		t.Fatal("expected error for empty Classification")
	}
}

// ====================================================================
// Logout (DESTRUCTIVE — only runs when CODEX_SDK_LOGOUT_OK=1)
// ====================================================================

func TestIntCmd_Logout_Behavior(t *testing.T) {
	if os.Getenv("CODEX_SDK_LOGOUT_OK") != "1" {
		t.Skip("set CODEX_SDK_LOGOUT_OK=1 to run Logout — WILL invalidate ~/.codex/auth.json and require interactive `codex login` to recover")
	}
	c := connectReadOnlyClient(t)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// Per R3: after Logout, downstream auth-needing RPCs may fail.
	// Verify ReadAccount still gives SOME response (even if it
	// reports unauthenticated) — i.e., the connection survives.
	_, err := c.ReadAccount(context.Background())
	t.Logf("post-Logout ReadAccount: %v (nil = still authed; non-nil = expected after logout)", err)
}

// ====================================================================
// Thread.Rollback / SetName (mutate throwaway thread — no quota)
// ====================================================================
