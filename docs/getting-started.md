# Getting Started

## Install

```bash
go get github.com/hishamkaram/codex-agent-sdk-go
```

Requires:
- Go 1.25+
- Codex CLI 0.121.0+ (lower versions work but trigger a soft warning —
  see `docs/wire-protocol.md`).
  - `npm install -g @openai/codex` — or Homebrew, per your OS.
- Authentication: one of
  - `OPENAI_API_KEY=sk-…` in the environment (pay-per-token), or
  - `~/.codex/auth.json` populated via `codex login` (ChatGPT Plus/Pro).
  - If both are set, the API key wins. Document this to your users to
    prevent surprise billing.

## Minimal query

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
    events, err := codex.Query(ctx, "Reply with exactly: OK",
        types.NewCodexOptions())
    if err != nil {
        log.Fatal(err)
    }
    for ev := range events {
        if c, ok := ev.(*types.ItemCompleted); ok {
            if msg, ok := c.Item.(*types.AgentMessage); ok {
                fmt.Println(msg.Content)
            }
        }
    }
}
```

`Query` spawns a fresh `codex app-server` subprocess, runs one turn, and
closes the subprocess after the event channel drains. For multi-turn
work, use `NewClient` + `StartThread`.

## Multi-turn

```go
client, err := codex.NewClient(ctx, types.NewCodexOptions())
if err != nil { log.Fatal(err) }
if err := client.Connect(ctx); err != nil { log.Fatal(err) }
defer client.Close(context.Background())

thread, err := client.StartThread(ctx, nil)
if err != nil { log.Fatal(err) }

turn1, _ := thread.Run(ctx, "What's 2+2?", nil)
fmt.Println(turn1.FinalResponse)

turn2, _ := thread.Run(ctx, "Now multiply by 5.", nil)
fmt.Println(turn2.FinalResponse)
```

`Thread.Run` is buffered — it blocks until `turn/completed`. For
streaming (deltas as they arrive), use `Thread.RunStreamed`.

## Streaming

```go
events, err := thread.RunStreamed(ctx, "Tell me a short story.", nil)
if err != nil { log.Fatal(err) }
for ev := range events {
    if u, ok := ev.(*types.ItemUpdated); ok {
        if d, ok := u.Delta.(*types.AgentMessageDelta); ok {
            fmt.Print(d.TextChunk)  // chunks arrive as the model generates
        }
    }
}
```

## Resume across process restarts

```go
// First process: start a thread, save its ID somewhere durable.
thread, _ := client.StartThread(ctx, nil)
persist(thread.ID())

// Later, in a fresh process:
client2, _ := codex.NewClient(ctx, types.NewCodexOptions())
client2.Connect(ctx)
resumed, err := client2.ResumeThread(ctx, persistedID,
    &types.ResumeOptions{Cwd: "/my/project"})
// resumed now continues the original conversation
```

The server reads the rollout file at
`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` to reconstruct history.

## Approval callbacks

The Codex server asks for approval before running commands or editing
files outside the sandbox. Register a callback via
`WithApprovalCallback`:

```go
opts := types.NewCodexOptions().
    WithSandbox(types.SandboxWorkspaceWrite).
    WithApprovalPolicy(types.ApprovalUntrusted).
    WithApprovalCallback(func(ctx context.Context, req types.ApprovalRequest) types.ApprovalDecision {
        if r, ok := req.(*types.CommandExecutionApprovalRequest); ok {
            if isSafe(r.Command) { return types.ApprovalAccept{} }
        }
        return types.ApprovalDeny{Reason: "not allowlisted"}
    })
```

If no callback is registered, the SDK defaults to deny — safer than
silent auto-approve. See `docs/approvals.md` for the full flow.

## Examples

Seven runnable examples live under `examples/` — see each directory's
`main.go` for the pattern:

- `simple_query`     — one-shot Query
- `streaming`        — multi-turn + delta streaming
- `resume`           — persistent thread across restarts
- `fork`             — branch from an existing thread
- `with_approvals`   — command/file approval callback
- `with_mcp`         — MCP server registration (stdio + HTTP)
- `structured_output` — JSON-schema-constrained final response

## Next steps

- `docs/architecture.md` — how the SDK layers compose
- `docs/wire-protocol.md` — JSON-RPC method reference
- `docs/approvals.md` — approval-request taxonomy
