# Hooks

Codex emits two hook-related wire notifications when its `codex_hooks`
feature flag is enabled:

- `hook/started` — a hook handler began running
- `hook/completed` — a hook handler finished (status may be `completed`,
  `failed`, `blocked`, or `stopped`)

Each carries a `HookRunSummary` with the event name (`preToolUse` |
`postToolUse` | `sessionStart` | `userPromptSubmit` | `stop`), handler
type, scope, duration, and any structured output entries.

This SDK supports hooks in TWO tiers:

## Tier 1: Observer mode

Receive typed `*types.HookStarted` / `*types.HookCompleted` events in
your event stream. Requires just one option:

```go
opts := types.NewCodexOptions().WithHooks(true)
client, _ := codex.NewClient(ctx, opts)
client.Connect(ctx)
thread, _ := client.StartThread(ctx, nil)

events, _ := thread.RunStreamed(ctx, "What's 2+2?", nil)
for ev := range events {
    switch e := ev.(type) {
    case *types.HookStarted:
        fmt.Printf("hook started: %s (%s)\n", e.Run.EventName, e.Run.HandlerType)
    case *types.HookCompleted:
        fmt.Printf("hook completed: %s status=%s\n", e.Run.EventName, e.Run.Status)
    }
}
```

This works against YOUR existing `~/.codex/hooks.json` configuration.
Whatever codex hooks you already run (command handlers, agent handlers,
prompt handlers) — the SDK observes them. Observer mode does NOT modify
your hooks.json.

## Tier 2: Programmatic Go callbacks

Register a Go function and the SDK takes care of everything else:

```go
opts := types.NewCodexOptions().
    WithHookCallback(func(ctx context.Context, in types.HookInput) types.HookDecision {
        if in.HookEventName == types.HookPreToolUse && in.ToolName == "Bash" {
            // in.ToolInput is raw JSON; parse if you need structured fields.
            return types.HookAllow{}
        }
        return types.HookAllow{}
    })

client, _ := codex.NewClient(ctx, opts)
client.Connect(ctx) // SDK starts socket + isolated CODEX_HOME
defer client.Close(ctx) // SDK removes isolated CODEX_HOME
```

### What Connect / Close do

On `Connect` (when `WithHookCallback` is set), the SDK:

1. Resolves the `codex-sdk-hook-shim` binary (PATH, `$GOPATH/bin`,
   `$HOME/go/bin`, `./.bin`, or `WithShimPath`).
2. Starts a Unix socket listener at
   `~/.cache/codex-sdk/hook-<pid>.sock`.
3. Creates a temporary isolated `CODEX_HOME`.
4. Writes generated `hooks.json` in that isolated `CODEX_HOME`.
5. Exports `CODEX_HOME=<tempdir>` and
   `CODEX_SDK_HOOK_SOCKET=<socket path>` to the codex
   subprocess so the shim can dial back.

On `Close`, the SDK removes the isolated `CODEX_HOME`.

To temporarily mutate user `~/.codex/hooks.json` instead, opt in
explicitly:

```go
opts := types.NewCodexOptions().
    WithHookCallback(handler).
    WithHookConfigMode(types.HookConfigModeUserHome)
```

In user-home mode, `Connect` backs up your existing
`~/.codex/hooks.json` (if any), writes generated config for the SDK
lifetime, and `Close` restores the original byte-for-byte.

If the SDK process is killed before `Close` runs (`kill -9`), the next
SDK user-home `Connect` finds the stale backup file (>60 seconds old),
restores it, and proceeds. Your data is never lost across crashes.

### Install the shim (one-time)

```bash
go install github.com/hishamkaram/codex-agent-sdk-go/cmd/codex-sdk-hook-shim@latest
# or in a checked-out SDK repo:
make install-shim
```

The shim is a zero-dep single-file binary. `which codex-sdk-hook-shim`
confirms it's on `$PATH`. Pin a non-PATH location with
`WithShimPath("/abs/path/to/shim")` if needed.

### Decision types

Return exactly one of:

```go
types.HookAllow{}                                   // proceed (default)
types.HookAllow{AdditionalContext: &ctx}            // inject model context (postToolUse, sessionStart, userPromptSubmit, stop)
types.HookAllow{SystemMessage: &msg}                // attach system-level note
types.HookDeny{Reason: "not allowlisted"}           // block the action
types.HookAsk{Reason: "defer to user"}              // preToolUse: fall through to ApprovalCallback
```

Notes on what each decision actually does under codex 0.121.0:

- `HookAllow{}` on PreToolUse — exits 0 silently; codex defaults to allow.
- `HookAllow{UpdatedInput: ...}` on PreToolUse — **silently dropped**.
  codex 0.121.0 rejects `updatedInput` (binary string evidence:
  `"PreToolUse hook returned unsupported updatedInput"`). The SDK keeps
  the field on `HookAllow` for forward compatibility but does not emit
  it to codex. Re-enabled when upstream supports it.
- `HookAllow{AdditionalContext: ...}` on PreToolUse — **silently dropped**.
  Same reason. Use `AdditionalContext` on PostToolUse / UserPromptSubmit /
  SessionStart / Stop instead.
- `HookDeny{}` on PreToolUse — emits `permissionDecision:"deny"` with
  the supplied reason; codex blocks the command.
- `HookDeny{}` on UserPromptSubmit / Stop — exit code 2 + stderr reason
  (codex's blocking convention).
- `HookAsk{}` on PreToolUse — exits 0 silently. codex 0.121.0 rejects
  `permissionDecision:"ask"`; the SDK lets codex's normal approval
  policy decide whether to fire `ApprovalCallback`. Use a sandbox /
  approval-policy combination that already gates the command (e.g.
  `SandboxReadOnly` + `ApprovalOnRequest`) for the fall-through to fire.

### HookInput fields

`types.HookInput` carries every field codex writes to the shim's stdin:

| Field | Populated for | Notes |
|---|---|---|
| `HookEventName` | every event | Normalized from PascalCase wire form to camelCase constants (e.g. `HookPreToolUse`). |
| `SessionID`, `TurnID`, `TranscriptPath`, `Cwd` | every event | Routing context. |
| `Model` | every event | Model identifier reported by the CLI. |
| `PermissionMode` | every event | `"bypassPermissions"`, `"untrusted"`, etc. |
| `ToolName`, `ToolInput`, `ToolUseID` | preToolUse, postToolUse | `ToolInput`/`ToolResult` are raw JSON. |
| `ToolResult` | postToolUse | Raw JSON. |
| `Prompt` | userPromptSubmit, sessionStart | The user's text. |
| `LastAssistantMessage` | stop | Final agent message. |
| `Source` | sessionStart | `"startup"` / `"resume"` / `"clear"`. |
| `Raw` | every event | Full payload for fields the SDK hasn't typed yet. |

### Concurrent SDK clients

Isolated hook mode supports multiple `WithHookCallback` Clients because
each Client gets its own `CODEX_HOME`.

User-home hook mode supports **one** `WithHookCallback`-enabled `Client`
per machine. A second overlapping user-home Client fails with
`concurrent codex SDK Client detected`. This prevents the silent
corruption that would happen if two backups chained.

If you need multiple Clients in one process, share a single
HookCallback-enabled Client and route from it.

### Caveats (upstream codex 0.121.0 limitations)

- **`codex exec` hangs** when hook config is present in the same
  `CODEX_HOME`. The default isolated SDK mode avoids mutating your user
  home, so manual `codex exec` in your normal shell is unaffected.
- **`PreToolUse.updatedInput` is rejected**. codex 0.121.0 acknowledges
  the field in its wire schema but logs `"PreToolUse hook returned
  unsupported updatedInput"` and does not consume it. The SDK silently
  drops it. Re-enabled when upstream lands the rewrite path.
- **`PreToolUse.permissionDecision:"allow"` and `:"ask"` are rejected.**
  Same upstream gap. The SDK emits empty stdout for both — codex
  defaults to allow, and `HookAsk` falls through to your normal
  `ApprovalPolicy`. To exercise the ask path, ensure the policy gates
  the command (e.g. `ApprovalOnRequest` + `SandboxReadOnly`).
- **`PreToolUse` only fires for the Bash tool** (upstream issue
  #16732). File edits via `apply_patch` don't trigger PreToolUse.
- **Hooks may fire multiple times** for the same action under codex
  retry paths (live testing observed `denyCount=4` for a single user
  prompt). Callbacks must be idempotent.
- **`SessionStart` DOES fire on `thread/start`** (verified live against
  codex 0.121.0). Earlier SDK docs claimed otherwise; this is fixed.

### Other notes

- Your callback runs on the SDK's listener goroutine. Don't call SDK
  methods that take the dispatcher lock (`Thread.Run`,
  `Thread.RunStreamed`) from inside the callback — that will deadlock.
  Forward to a separate goroutine via a channel if needed.
- Default callback timeout is 30s (`WithHookTimeout` to override). If
  the callback panics or times out, the SDK fails open (`HookAllow`)
  so codex never bricks.
- Windows: Unix sockets aren't supported; hooks mode is Linux/macOS only.

## References

- [upstream codex hooks docs](https://developers.openai.com/codex/hooks)
- `docs/wire-protocol.md` for the full event taxonomy
- `types/hooks.go` for the Go type definitions
- `cmd/codex-sdk-hook-shim/main.go` for the shim source
