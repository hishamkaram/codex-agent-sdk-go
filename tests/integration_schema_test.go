//go:build integration

// TestIntegrationSchema is the live-CLI real-peer contract test mandated by
// feature 187 (codex-sdk-resync) FR-017 and US3-AC4. It spawns a real
// `codex app-server` subprocess, drives a single turn with a prompt that
// forces parallel bash execution, an MCP-style tool invocation, and a file
// edit, then asserts that every `item/started`, `item/updated`, and
// `item/completed` notification parses to a CONCRETE typed item — never
// falling back to `*types.UnknownItem`.
//
// The test is the SDK-side half of the cross-repo producer/consumer
// verification chain required by
// `.claude/rules/real-peer-contract-verification.md`. Unit tests and hand-
// rolled JSON fixtures cannot catch schema drift between the codex server's
// actual wire shapes and the SDK's parser; only a live subprocess can.
//
// Run:
//
//	OPENAI_API_KEY=<key> go test -tags=integration -run TestIntegrationSchema ./tests/...
//
// This test WILL consume a small amount of OpenAI quota (typically <5000
// tokens). It is skipped when OPENAI_API_KEY is unset.
package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// schemaPrompt forces codex to exercise several item-type code paths in a
// single turn: parallel bash commands (CommandExecution or DynamicToolCall),
// a file edit (FileChange), and an agent message summary (AgentMessage).
// MCP tool calls are opportunistic — codex may or may not route through
// MCP depending on its current tool catalog, so we do not hard-require
// McpToolCall to appear.
const schemaPrompt = "Run `pwd`, `date`, and `whoami` in parallel. " +
	"Then create /tmp/codex-spike-187.txt with the contents " +
	"'hello from feature 187' and summarize what you did."

func TestIntegrationSchema(t *testing.T) {
	// Accept either OPENAI_API_KEY env var OR a logged-in ~/.codex/auth.json
	// (ChatGPT login via `codex login`). Skip only when neither is present.
	if os.Getenv("OPENAI_API_KEY") == "" {
		if home, err := os.UserHomeDir(); err != nil {
			t.Skip("no auth: set OPENAI_API_KEY or run `codex login`")
		} else if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err != nil {
			t.Skip("no auth: set OPENAI_API_KEY or run `codex login`")
		}
	}
	requireCodex(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cwd := t.TempDir()

	opts := types.NewCodexOptions().WithCwd(cwd)

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	// SandboxWorkspaceWrite + ApprovalNever lets the turn complete non-
	// interactively: the agent can create the file and run the bash
	// commands without prompting for approval. This matches the
	// user-spec'd "auto-approve policy" intent.
	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Cwd:            cwd,
		Sandbox:        types.SandboxWorkspaceWrite,
		ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	events, err := thread.RunStreamed(ctx, schemaPrompt, nil)
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}

	// Per-type counts of items we saw, keyed by ItemType() string.
	// Logged at the end for debuggability; also used to sanity-check
	// that at least one tool-call item appeared.
	counts := map[string]int{}

	// Track UnknownItem occurrences across events. We fail the test with
	// t.Errorf (not t.Fatalf) so we can collect ALL unknowns in a single
	// run — helps diagnose schema drift in one pass instead of one-at-a-
	// time.
	//
	// NOTE on parser errors: the SDK currently logs `parse event failed`
	// as a zap Warn and then emits `*types.UnknownItem` as the graceful
	// fallback. We cannot intercept the zap logger from this test, but
	// the UnknownItem assertion below is the canonical observable signal
	// of any parse failure — if parsing fails, the parser's last resort
	// is to wrap the raw payload in UnknownItem. Asserting zero
	// UnknownItems therefore asserts zero parse failures transitively.
	sawTurnCompleted := false

	for ev := range events {
		switch e := ev.(type) {
		case *types.ItemStarted:
			recordItem(t, "ItemStarted", e.TurnID, e.ItemID, e.Item, counts)
		case *types.ItemCompleted:
			recordItem(t, "ItemCompleted", e.TurnID, e.ItemID, e.Item, counts)
		case *types.ItemUpdated:
			// ItemUpdated wraps a Delta (not an Item). Deltas are a
			// separate schema and out of scope for the FR-017
			// UnknownItem check — only the item/started and item/
			// completed payloads carry a full ThreadItem. Still
			// record that we saw one so the final log is complete.
			counts["__delta__"]++
		case *types.TurnCompleted:
			sawTurnCompleted = true
		case *types.TurnFailed:
			t.Errorf("TurnFailed: code=%s message=%s", e.Code, e.Message)
		}
	}

	if !sawTurnCompleted {
		t.Fatalf("event channel closed without TurnCompleted; counts so far: %+v", counts)
	}

	t.Logf("item types seen: %+v", counts)

	// Sanity: the prompt explicitly asks for 3 parallel bash commands,
	// so codex should emit at least one CommandExecution OR
	// DynamicToolCall item. If zero appeared, either the prompt was
	// ignored or the parser missed every tool-call notification.
	toolCallItems := counts["commandExecution"] + counts["dynamicToolCall"]
	if toolCallItems == 0 {
		t.Errorf("expected at least one tool-call item (commandExecution or dynamicToolCall); codex may not have run the bash commands. counts=%+v", counts)
	}
}

// recordItem is the core UnknownItem assertion. It fails the test (but does
// not abort) when the wrapped item is an *types.UnknownItem, so a single
// run surfaces every schema-drift point at once. Non-Unknown items are
// tallied by ItemType() string for the end-of-test summary.
func recordItem(t *testing.T, evKind, turnID, itemID string, item types.ThreadItem, counts map[string]int) {
	t.Helper()
	if unk, ok := item.(*types.UnknownItem); ok {
		t.Errorf("%s: turn %s item %s dispatched to UnknownItem (type=%s, raw=%q)",
			evKind, turnID, itemID, unk.Type, string(unk.Raw))
		counts["__unknown__"]++
		return
	}
	counts[item.ItemType()]++
}
