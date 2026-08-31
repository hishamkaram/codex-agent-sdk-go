//go:build integration

package tests

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

const liveBackgroundCommand = "sleep 120"

type liveBackgroundRunResult struct {
	turn *codex.Turn
	err  error
}

// TestIntegration_BackgroundTerminalLifecycle is the release gate for Codex
// background controls. It correlates the shell item's process ID with the
// provider inventory, proves clean-and-drain, and verifies that cleaning
// terminals does not interrupt the parent thread. process/exited is not part
// of this lifecycle; that notification belongs to client-created process/spawn
// handles, which are a separate app-server namespace.
func TestIntegration_BackgroundTerminalLifecycle(t *testing.T) {
	requireRunTurns(t)
	cliPath := requireCodex(t)
	requireAuth(t)
	requireExpectedCodexVersion(t, cliPath)

	workspace := t.TempDir()
	gitInit := exec.Command("git", "init", "--quiet", workspace)
	if output, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	opts := types.NewCodexOptions().
		WithCLIPath(cliPath).
		WithExperimentalAPI(true).
		WithModel("gpt-5.4").
		WithCwd(workspace)

	features, err := codex.DiscoverRuntimeFeatures(ctx, opts)
	if err != nil {
		t.Fatalf("DiscoverRuntimeFeatures: %v", err)
	}
	if !features.BackgroundTerminalInventory || !features.BackgroundTerminalsClean {
		t.Fatalf("required background terminal features unavailable: %+v", features)
	}

	client, err := codex.NewClient(ctx, opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	thread, err := client.StartThread(ctx, &types.ThreadOptions{
		Cwd:            workspace,
		Sandbox:        types.SandboxDangerFullAccess,
		ApprovalPolicy: types.ApprovalNever,
		Model:          "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	t.Cleanup(func() {
		archiveCtx, archiveCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer archiveCancel()
		if err := client.ArchiveThread(archiveCtx, thread.ID()); err != nil {
			t.Logf("WARN: archive live background thread %q: %v", thread.ID(), err)
		}
	})

	eventCtx, eventCancel := context.WithCancel(ctx)
	defer eventCancel()
	events, err := client.SubscribeThreadEvents(eventCtx, 512)
	if err != nil {
		t.Fatalf("SubscribeThreadEvents: %v", err)
	}

	runDone := make(chan liveBackgroundRunResult, 1)
	go func() {
		turn, runErr := thread.Run(
			ctx,
			"Use the shell execution tool once to run exactly `sleep 120`. Wait for it, then briefly report the result.",
			nil,
		)
		runDone <- liveBackgroundRunResult{turn: turn, err: runErr}
	}()

	started := waitForLiveCommandStart(t, ctx, events, runDone)
	terminal := waitForLiveBackgroundTerminal(t, ctx, client, thread.ID(), started.ItemID, runDone)
	if terminal.ProcessID == "" {
		t.Fatal("provider returned an empty background process handle")
	}
	startedCommand := started.Item.(*types.CommandExecution)
	if startedCommand.ProcessID != terminal.ProcessID {
		t.Fatalf("item/started process ID = %q, inventory process ID = %q", startedCommand.ProcessID, terminal.ProcessID)
	}

	if err := thread.CleanBackgroundTerminals(ctx); err != nil {
		t.Fatalf("CleanBackgroundTerminals: %v", err)
	}
	waitForBackgroundInventoryDrain(t, ctx, client, thread.ID(), terminal.ProcessID)

	firstRun := waitForLiveRun(t, ctx, runDone)
	if firstRun.err != nil {
		t.Fatalf("controlled background turn: %v", firstRun.err)
	}
	requireTerminalCommandCorrelation(t, firstRun.turn, terminal)

	followupCtx, followupCancel := context.WithTimeout(ctx, 90*time.Second)
	defer followupCancel()
	followup, err := thread.Run(followupCtx, "Reply with exactly: CODEX_BACKGROUND_CONTROL_OK", nil)
	if err != nil {
		t.Fatalf("parent thread follow-up: %v", err)
	}
	if !strings.Contains(followup.FinalResponse, "CODEX_BACKGROUND_CONTROL_OK") {
		t.Fatalf("parent thread follow-up response = %q", followup.FinalResponse)
	}
	if followup.Usage.InputTokens == 0 && followup.Usage.OutputTokens == 0 {
		t.Fatal("parent thread follow-up reported no usage evidence")
	}

	t.Logf(
		"Codex live background lifecycle thread=%q item=%q process=%q usage_input=%d usage_output=%d first_turn_status=%q",
		thread.ID(),
		terminal.ItemID,
		terminal.ProcessID,
		followup.Usage.InputTokens,
		followup.Usage.OutputTokens,
		firstRun.turn.Status,
	)
}

func requireExpectedCodexVersion(t *testing.T, cliPath string) {
	t.Helper()
	output, err := exec.Command(cliPath, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v: %s", cliPath, err, output)
	}
	version := strings.TrimSpace(string(output))
	if expected := strings.TrimSpace(os.Getenv("CODEX_SDK_EXPECT_CLI_VERSION")); expected != "" && !strings.Contains(version, expected) {
		t.Fatalf("codex version = %q, want %q", version, expected)
	}
	t.Logf("Codex live CLI: %s (%s)", version, cliPath)
}

func waitForLiveCommandStart(
	t *testing.T,
	ctx context.Context,
	events <-chan codex.ThreadEventEnvelope,
	runDone <-chan liveBackgroundRunResult,
) *types.ItemStarted {
	t.Helper()
	for {
		select {
		case envelope, ok := <-events:
			if !ok {
				t.Fatal("thread event subscription closed before command start")
			}
			if envelope.Err != nil {
				t.Fatalf("thread event subscription gap before command start: %v", envelope.Err)
			}
			started, ok := envelope.Event.(*types.ItemStarted)
			if !ok {
				continue
			}
			command, ok := started.Item.(*types.CommandExecution)
			if ok && strings.Contains(command.Command, liveBackgroundCommand) {
				return started
			}
		case result := <-runDone:
			t.Fatalf("turn ended before a live command was observed: %v", result.err)
		case <-ctx.Done():
			t.Fatalf("wait for command start: %v", ctx.Err())
		}
	}
}

func waitForLiveBackgroundTerminal(
	t *testing.T,
	ctx context.Context,
	client *codex.Client,
	threadID string,
	itemID string,
	runDone <-chan liveBackgroundRunResult,
) types.BackgroundTerminal {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		terminals, err := client.ListBackgroundTerminals(ctx, threadID)
		if err != nil {
			t.Fatalf("ListBackgroundTerminals: %v", err)
		}
		for _, terminal := range terminals {
			if terminal.ItemID == itemID {
				return terminal
			}
		}
		select {
		case result := <-runDone:
			t.Fatalf("turn ended before its background terminal was listed: %v", result.err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for background terminal: %v", ctx.Err())
		}
	}
}

func waitForBackgroundInventoryDrain(
	t *testing.T,
	ctx context.Context,
	client *codex.Client,
	threadID string,
	processID string,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		terminals, err := client.ListBackgroundTerminals(ctx, threadID)
		if err != nil {
			t.Fatalf("ListBackgroundTerminals after clean: %v", err)
		}
		found := false
		for _, terminal := range terminals {
			if terminal.ProcessID == processID {
				found = true
				break
			}
		}
		if !found {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for background terminal inventory drain: %v", ctx.Err())
		}
	}
}

func waitForLiveRun(
	t *testing.T,
	ctx context.Context,
	runDone <-chan liveBackgroundRunResult,
) liveBackgroundRunResult {
	t.Helper()
	select {
	case result := <-runDone:
		return result
	case <-ctx.Done():
		t.Fatalf("wait for controlled background turn: %v", ctx.Err())
		return liveBackgroundRunResult{}
	}
}

func requireTerminalCommandCorrelation(t *testing.T, turn *codex.Turn, terminal types.BackgroundTerminal) {
	t.Helper()
	if turn == nil {
		t.Fatal("controlled background turn is nil")
	}
	for _, item := range turn.Items {
		command, ok := item.(*types.CommandExecution)
		if !ok || command.ID != terminal.ItemID {
			continue
		}
		if command.ProcessID != terminal.ProcessID {
			t.Fatalf("item/completed process ID = %q, inventory process ID = %q", command.ProcessID, terminal.ProcessID)
		}
		if command.Status == "" || command.Status == "inProgress" {
			t.Fatalf("controlled command ended with non-terminal status %q", command.Status)
		}
		return
	}
	t.Fatalf("controlled command item %q missing from terminal turn", terminal.ItemID)
}
