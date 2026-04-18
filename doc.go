// Package codex provides a Go SDK for the OpenAI Codex CLI app-server transport.
//
// The SDK spawns `codex app-server` as a child process and communicates with it
// over JSON-RPC 2.0 on stdio. It exposes two ways to interact with Codex:
//
// 1. Query function for simple, one-shot interactions:
//
//	ctx := context.Background()
//	opts := types.NewCodexOptions().WithModel("gpt-5.4")
//	events, err := Query(ctx, "Summarize the repo", opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for event := range events {
//	    if completed, ok := event.(*types.ItemCompleted); ok {
//	        if msg, ok := completed.Item.(*types.AgentMessage); ok {
//	            fmt.Println(msg.Text)
//	        }
//	    }
//	}
//
// 2. Codex client for interactive, multi-turn conversations:
//
//	ctx := context.Background()
//	client, err := NewClient(ctx, types.NewCodexOptions())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close(ctx)
//
//	thread, err := client.StartThread(ctx, &types.ThreadOptions{Cwd: "/my/project"})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	events, _ := thread.RunStreamed(ctx, "Make a plan")
//	for event := range events { /* ... */ }
//
//	// continue on the same thread
//	events2, _ := thread.RunStreamed(ctx, "Now implement it")
//	for event := range events2 { /* ... */ }
//
// Query vs Codex client:
//
// Use Query when:
//   - You have a simple, one-off prompt
//   - You don't need multi-turn context
//   - You don't need fine-grained approval callbacks
//
// Use Codex client when:
//   - You need multi-turn conversations with follow-ups
//   - You want to resume a thread across process restarts
//   - You need approval callbacks for server-initiated tool-use requests
//   - You need turn interruption
//
// Thread lifecycle:
//
//	thread, _ := client.StartThread(ctx, opts)   // new thread
//	thread, _ = client.ResumeThread(ctx, id, opts) // resume persisted thread
//	threads, _ := client.ListThreads(ctx)          // enumerate persisted threads
//	fork, _ := client.ForkThread(ctx, id, opts)    // branch from existing thread
//	client.ArchiveThread(ctx, id)                   // mark archived
//
// Error handling:
//
// The SDK provides typed errors for common failure scenarios:
//
//	if err := client.Connect(ctx); err != nil {
//	    if types.IsCLINotFoundError(err) {
//	        log.Fatal("Codex CLI not installed. Run: npm install -g @openai/codex")
//	    }
//	    if types.IsCLIConnectionError(err) {
//	        log.Fatal("Failed to start codex app-server:", err)
//	    }
//	    log.Fatal("Unexpected error:", err)
//	}
//
// Approval callbacks:
//
// Codex sends server-initiated approval requests when the model wants to run a
// potentially-sensitive action. Register a callback to decide:
//
//	opts := types.NewCodexOptions().
//	    WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
//	        if r, ok := req.(*types.CommandExecutionApprovalRequest); ok {
//	            if isSafeCommand(r.Command) {
//	                return types.ApprovalAccept{}
//	            }
//	        }
//	        return types.ApprovalDeny{Reason: "not on allowlist"}
//	    })
//
// Context cancellation:
//
// All operations respect context cancellation for clean shutdown. Canceling the
// context passed to Query or Run will drain the subprocess, close stdin, and wait
// for process exit (with a timeout).
//
// For more examples, see the examples/ directory.
package codex
