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

## Tier 1: Observer mode (fully supported in v0.2.0)

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
prompt handlers) — the SDK observes them.

## Tier 2: Programmatic Go callbacks (experimental in v0.2.0)

v0.2.0 ships the infrastructure for programmatic hooks but DOES NOT
auto-configure codex to invoke it. The reason: upstream codex rejects
tempdir `CODEX_HOME` paths and has hooks.json parsing quirks that
block turn startup when the SDK writes a generated config. The
auto-wiring lands in v0.3.0.

### What you get in v0.2.0

When you call `WithHookCallback(handler)`:

1. The SDK starts a Unix socket listener at
   `~/.cache/codex-sdk/hook-<pid>.sock`.
2. The SDK exports `CODEX_SDK_HOOK_SOCKET=<path>` in the subprocess env.
3. Your handler runs when something dials the socket with a valid
   `HookRequest` frame.

### DIY: make codex actually fire your Go callback

You have to tell codex to run `codex-sdk-hook-shim` as a hook handler.
That means adding entries to `~/.codex/hooks.json` (or the project
`.codex/hooks.json`) pointing at the shim binary.

**Install the shim** (one-time):

```bash
go install github.com/hishamkaram/codex-agent-sdk-go/cmd/codex-sdk-hook-shim@latest
# or in a checked-out SDK repo:
make install-shim
```

The shim is a zero-dep single-file binary. `which codex-sdk-hook-shim`
confirms it's on `$PATH`.

**Add entries to `~/.codex/hooks.json`**:

```json
{
  "hooks": {
    "preToolUse": [
      {"matcher": "*",
       "hooks": [{"type": "command",
                  "command": "/path/to/codex-sdk-hook-shim",
                  "timeout": 30000}]}
    ],
    "postToolUse":      [{"matcher": "*", "hooks": [{"type": "command", "command": "/path/to/codex-sdk-hook-shim", "timeout": 30000}]}],
    "sessionStart":     [{"matcher": "*", "hooks": [{"type": "command", "command": "/path/to/codex-sdk-hook-shim", "timeout": 30000}]}],
    "userPromptSubmit": [{"matcher": "*", "hooks": [{"type": "command", "command": "/path/to/codex-sdk-hook-shim", "timeout": 30000}]}],
    "stop":             [{"matcher": "*", "hooks": [{"type": "command", "command": "/path/to/codex-sdk-hook-shim", "timeout": 30000}]}]
  }
}
```

**Enable the feature flag**:

```go
opts := types.NewCodexOptions().
    WithHooks(true).                                             // --enable codex_hooks
    WithHookCallback(func(ctx context.Context, in types.HookInput) types.HookDecision {
        if in.HookEventName == types.HookPreToolUse {
            // Your Go code: inspect in.ToolInput["command"], etc.
            return types.HookAllow{}
        }
        return types.HookAllow{}
    })
```

When codex fires a hook, it spawns `codex-sdk-hook-shim`. The shim reads
`CODEX_SDK_HOOK_SOCKET` (set automatically by the SDK on Connect),
forwards the hook payload, and your Go callback decides.

### Decision types

Return exactly one of:

```go
types.HookAllow{}                                   // proceed (default)
types.HookAllow{UpdatedInput: json.RawMessage(...)} // preToolUse only: rewrite the command
types.HookAllow{AdditionalContext: &ctx}            // inject model context (userPromptSubmit, etc.)
types.HookDeny{Reason: "not allowlisted"}           // block the action
types.HookAsk{Reason: "defer to user"}              // preToolUse only: fall through to approval callback
```

### Caveats

- The shim binary must be on `$PATH` or pinned via `WithShimPath`.
- Your callback runs on the SDK dispatcher goroutine. Don't call SDK
  methods that would deadlock.
- Default callback timeout is 30s (`WithHookTimeout` to override).
- If the callback panics or times out, the SDK fails open (`HookAllow`)
  so codex never bricks.
- Upstream docs note "PreToolUse only supports Bash tool interception"
  — file edits won't fire preToolUse hooks.
- Windows: Unix sockets aren't supported; hooks mode is Linux/macOS
  only in v0.2.0.

## What's coming in v0.3.0

- Full auto-wiring: SDK writes hooks.json into a codex-acceptable
  location, no DIY setup required.
- TCP socket fallback for Windows.
- Merging with existing user hooks.json instead of overriding.

## References

- [upstream codex hooks docs](https://developers.openai.com/codex/hooks)
- `docs/wire-protocol.md` for the full event taxonomy
- `types/hooks.go` for the Go type definitions
- `cmd/codex-sdk-hook-shim/main.go` for the shim source
