# Codex Agent SDK for Go

[![Go 1.25](https://img.shields.io/badge/Go-1.25-00add8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Go SDK for the OpenAI Codex CLI **app-server** transport — spawns `codex app-server` as a child process, speaks JSON-RPC 2.0 over stdio, and exposes a typed Go API for threads, turns, streaming events, approvals, and MCP configuration.

> **Status**: v0.1.0 preview. API may change before v1.0.0. Feedback welcome.

Sibling SDK: [`claude-agent-sdk-go`](https://github.com/hishamkaram/claude-agent-sdk-go) does the same thing for the Claude Code CLI.

## Why this SDK?

Codex's app-server exposes a JSON-RPC 2.0 protocol over stdio — bidirectional, stateful, with server-initiated approval requests. Consuming it directly means handling line-framing with a 2 MiB minimum buffer, demultiplexing three request shapes (responses, notifications, server-initiated requests), serializing concurrent turns to preserve event boundaries, and mapping ~15 notification types to typed Go events. This SDK handles all of that and exposes a clean, typed API.

## Feature matrix

| Feature | Status (v0.1.0) |
|---|---|
| `codex app-server` transport | ✅ |
| `codex exec --json` one-shot | ❌ deferred to v2 |
| Thread start / resume / fork / archive / list | ✅ |
| `thread.Run()` (buffered) + `thread.RunStreamed()` (channel) | ✅ |
| Streaming events: turn/started, turn/completed, item/started, item/updated, item/completed, error, tokenUsage, compaction | ✅ |
| ThreadItem variants: agent_message, user_message, command_execution, file_change, mcp_tool_call, web_search, memory_read/write, plan, reasoning | ✅ |
| Input variants: text, localImage | ✅ |
| Sandbox modes: read-only, workspace-write, danger-full-access | ✅ |
| Approval policies: auto, read-only, untrusted, never, on-request | ✅ |
| Approval callback (server-initiated request → caller decides) | ✅ |
| MCP server config (stdio + streamable HTTP) | ✅ |
| JSON-schema structured output | ✅ |
| Typed errors with `Is*()` helpers | ✅ |
| Turn interrupt | ✅ |
| CLI discovery + soft version probe | ✅ |
| Goroutine leak detection (goleak) | ✅ |
| Hooks (pre/post-tool callbacks) | ❌ upstream doesn't expose SDK-side registration yet |
| Slash commands | ❌ CLI-TUI only |
| Native FFI (CGO) | ❌ deferred |

## Prerequisites

- Go 1.25+
- Codex CLI installed: `npm install -g @openai/codex` (or your distro's equivalent)
- Auth (one of):
  - `OPENAI_API_KEY` environment variable (pay-per-token)
  - `~/.codex/auth.json` (ChatGPT Plus/Pro subscription; run `codex login` once outside the daemon)

## Install

```bash
go get github.com/hishamkaram/codex-agent-sdk-go
```

## Quick start

### One-shot query

```go
package main

import (
	"context"
	"fmt"
	"log"

	codex "github.com/hishamkaram/codex-agent-sdk-go"
	"github.com/hishamkaram/codex-agent-sdk-go/types"
)

func main() {
	ctx := context.Background()
	opts := types.NewCodexOptions().
		WithModel("gpt-5.4").
		WithSandbox(types.SandboxReadOnly).
		WithApprovalPolicy(types.ApprovalAuto)

	events, err := codex.Query(ctx, "Summarize the repo in the current directory", opts)
	if err != nil {
		log.Fatal(err)
	}
	for event := range events {
		switch e := event.(type) {
		case *types.ItemCompleted:
			if msg, ok := e.Item.(*types.AgentMessage); ok {
				fmt.Println(msg.Content)
			}
		case *types.TurnCompleted:
			fmt.Printf("Tokens: in=%d out=%d\n", e.Usage.InputTokens, e.Usage.OutputTokens)
		}
	}
}
```

### Interactive multi-turn client

```go
client, err := codex.NewClient(ctx, opts)
if err != nil { log.Fatal(err) }
defer client.Close(ctx)

thread, err := client.StartThread(ctx, &types.ThreadOptions{Cwd: "/my/project"})
if err != nil { log.Fatal(err) }

events, _ := thread.RunStreamed(ctx, "Make a plan to fix the CI failure")
for event := range events { /* ... */ }

events2, _ := thread.RunStreamed(ctx, "Now implement the plan")
for event := range events2 { /* ... */ }
```

### Approval callback

```go
opts = opts.WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
	if r, ok := req.(*types.CommandExecutionApprovalRequest); ok {
		if isSafeCommand(r.Command) {
			return types.ApprovalAccept{}
		}
	}
	return types.ApprovalDeny{Reason: "not on allowlist"}
})
```

## What it does

- Spawns `codex app-server` as a subprocess with stdin/stdout pipes
- Frames JSON-RPC 2.0 messages with LF termination (`jsonrpc` field omitted on wire)
- Demultiplexes responses (id → pending chan), notifications (→ events chan), server-initiated requests (→ approval callback)
- Serializes all stdin writes via single `stdinMu` to prevent frame interleave
- Serializes per-thread `Run()` calls via `turnMu` to preserve turn boundaries
- Maintains a 2 MiB read buffer for large notification payloads
- Translates raw JSON-RPC notifications into typed Go events
- Handles CLI discovery (PATH, `~/.codex/bin`, brew, npm install paths) and soft version probe
- Emits structured logs via zap

## What it does NOT do (v0.1.0)

- `codex exec --json` (fire-and-forget) transport — the `app-server` path is the only one implemented
- Lifecycle hooks (Codex upstream doesn't expose SDK-side pre/post-tool hook registration yet)
- CLI slash commands (those live in the interactive TUI, irrelevant to SDK callers)
- Codex-as-MCP-server mode (experimental upstream)
- Dynamic OpenAI pricing table (use static rates in this SDK; fetch at integration time if needed)

## Roadmap

See `/home/hesham/.claude/plans/jaunty-crafting-flamingo.md` (local) for the full scaffolding + feature-slice plan.

## License

MIT — see [LICENSE](LICENSE).
