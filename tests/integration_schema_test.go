//go:build integration

// TestIntegrationSchema is the live-CLI real-peer contract test mandated by
// feature 187 (codex-sdk-resync) FR-017 and US3-AC4. It spawns a real
// `codex app-server` subprocess, drives a single turn with a file edit and
// final agent message, then asserts that every `item/started`, `item/updated`, and
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
//	go test -tags=integration -run TestIntegrationSchema ./tests/...
//
// This test WILL consume a small amount of OpenAI quota (typically <5000
// tokens). It accepts OPENAI_API_KEY or an existing `codex login` session and
// is skipped when neither authentication source is available.
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
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/internal/transport"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

// schemaPrompt exercises deterministic FileChange and AgentMessage paths. Tool
// selection outside those outcomes is model-owned and is not a wire invariant.
const schemaPrompt = "Use the file-edit operation, not a shell command, to create schema-proof.txt " +
	"in the current working directory with the exact contents 'schema parser proof'. Do not inspect " +
	"other files or run tests. Finish by replying exactly SCHEMA_OK."

const (
	workspaceWriteSandboxConfig     = `sandbox_mode="workspace-write"`
	schemaSandboxPreflightTimeout   = 15 * time.Second
	schemaSandboxPreflightWaitDelay = 500 * time.Millisecond
)

var (
	errSchemaWorkspaceWriteBwrapUnavailable = errors.New("workspace-write Bubblewrap unavailable")
	errSchemaWorkspaceWriteUnavailable      = errors.New("workspace-write sandbox unavailable")
)

func TestIntegrationSchema(t *testing.T) {
	requireSchemaTestAuth(t)
	requireSchemaWorkspaceWriteSandbox(t, requireCodex(t))

	const schemaTurnTimeout = 180 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), schemaTurnTimeout)
	defer cancel()

	cwd := schemaWorkspace(t)
	thread := startSchemaTestThread(t, ctx, cwd)
	events, err := thread.RunStreamed(ctx, schemaPrompt, nil)
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}
	evidence := collectSchemaItems(t, events)
	assertSchemaOutcome(t, evidence, ctx.Err(), schemaTurnTimeout, cwd)
}

func requireSchemaWorkspaceWriteSandbox(t *testing.T, cliPath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), schemaSandboxPreflightTimeout)
	defer cancel()
	if err := schemaWorkspaceWriteSandboxError(ctx, cliPath); err != nil {
		if errors.Is(err, errSchemaWorkspaceWriteBwrapUnavailable) {
			t.Fatal("Codex workspace-write sandbox cannot initialize on this host: Bubblewrap loopback setup is denied. Resolve the Codex/Linux user-namespace policy, then rerun this test.")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Codex workspace-write sandbox preflight exceeded %s; resolve the local Codex sandbox policy, then rerun this test.", schemaSandboxPreflightTimeout)
		}
		t.Fatal("Codex workspace-write sandbox preflight failed; run `codex sandbox -c 'sandbox_mode=\"workspace-write\"' /bin/true` for details.")
	}
}

func schemaWorkspaceWriteSandboxError(ctx context.Context, cliPath string) error {
	// Use the same bounded retry path as other short-lived CLI probes. A
	// timed-out parent can leave descendants holding output pipes, and a freshly
	// installed CLI can transiently return ETXTBSY while its executable changes.
	stdout, stderr, err := transport.RunCLICommand(
		ctx,
		cliPath,
		nil,
		schemaSandboxPreflightWaitDelay,
		"sandbox",
		"-c",
		workspaceWriteSandboxConfig,
		"/bin/true",
	)
	output := []byte(stdout + stderr)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", errSchemaWorkspaceWriteUnavailable, ctxErr)
	}
	if bytes.Contains(output, []byte("bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted")) {
		return fmt.Errorf("%w: %w", errSchemaWorkspaceWriteBwrapUnavailable, err)
	}
	return fmt.Errorf("%w: %w", errSchemaWorkspaceWriteUnavailable, err)
}

func schemaWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	if err := exec.Command("git", "init", "--quiet", workspace).Run(); err != nil {
		t.Fatalf("initialize schema workspace: %v", err)
	}
	return workspace
}

func requireSchemaTestAuth(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") != "" {
		return
	}
	authFile, err := schemaAuthFile()
	if err != nil {
		t.Skip("no auth: set OPENAI_API_KEY or run `codex login`")
	}
	if _, err := os.Stat(authFile); err != nil {
		t.Skip("no auth: set OPENAI_API_KEY or run `codex login`")
	}
}

func schemaAuthFile() (string, error) {
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return filepath.Join(codexHome, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

func startSchemaTestThread(t *testing.T, ctx context.Context, cwd string) *codex.Thread {
	t.Helper()
	client, err := codex.NewClient(ctx, integrationOptions(t).WithCwd(cwd))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Cwd: cwd, Sandbox: types.SandboxWorkspaceWrite, ApprovalPolicy: types.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	return thread
}

type schemaItemEvidence struct {
	counts           map[string]int
	sawTurnCompleted bool
	sawProofChange   bool
}

func collectSchemaItems(t *testing.T, events <-chan types.ThreadEvent) schemaItemEvidence {
	t.Helper()
	evidence := schemaItemEvidence{counts: map[string]int{}}
	for event := range events {
		switch e := event.(type) {
		case *types.ItemStarted:
			recordItem(t, "ItemStarted", e.TurnID, e.ItemID, e.Item, evidence.counts)
		case *types.ItemCompleted:
			recordItem(t, "ItemCompleted", e.TurnID, e.ItemID, e.Item, evidence.counts)
			if change, ok := e.Item.(*types.FileChange); ok {
				t.Logf("completed fileChange status=%q parts=%d", change.Status, len(change.Changes))
				if isSchemaProofChange(change) {
					evidence.sawProofChange = true
				}
			}
		case *types.ItemUpdated:
			// ItemUpdated wraps a Delta (not an Item). Deltas are a
			// separate schema and out of scope for the FR-017
			// UnknownItem check — only the item/started and item/
			// completed payloads carry a full ThreadItem. Still
			// record that we saw one so the final log is complete.
			evidence.counts["__delta__"]++
		case *types.TurnCompleted:
			evidence.sawTurnCompleted = true
		case *types.TurnFailed:
			t.Errorf("TurnFailed: code=%s message=%s", e.Code, e.Message)
		}
	}
	return evidence
}

func assertSchemaOutcome(
	t *testing.T,
	evidence schemaItemEvidence,
	contextErr error,
	timeout time.Duration,
	workspace string,
) {
	t.Helper()
	if !evidence.sawTurnCompleted {
		if contextErr != nil {
			t.Fatalf("live turn timed out after %s before TurnCompleted; counts so far: %+v", timeout, evidence.counts)
		}
		t.Fatalf("event channel closed without TurnCompleted; counts so far: %+v", evidence.counts)
	}
	t.Logf("item types seen: %+v", evidence.counts)
	if evidence.counts["fileChange"] == 0 || evidence.counts["agentMessage"] == 0 {
		t.Errorf("expected concrete fileChange and agentMessage items; counts=%+v", evidence.counts)
	}
	if !evidence.sawProofChange {
		t.Errorf("completed fileChange did not preserve schema-proof path, kind, and diff")
		return
	}
	contents, err := os.ReadFile(filepath.Join(workspace, "schema-proof.txt"))
	if err != nil {
		t.Errorf("read completed schema proof: %v", err)
		return
	}
	if got, want := strings.TrimSpace(string(contents)), "schema parser proof"; got != want {
		t.Errorf("schema proof contents = %q, want %q", got, want)
	}
}

func isSchemaProofChange(change *types.FileChange) bool {
	if change.Status != "completed" {
		return false
	}
	for _, part := range change.Changes {
		if filepath.Base(part.Path) != "schema-proof.txt" || part.Kind == nil {
			continue
		}
		if (part.Kind.Type == "add" || part.Kind.Type == "update") &&
			strings.Contains(part.Diff, "schema parser proof") {
			return true
		}
	}
	return false
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
