# Architecture

The SDK is four cleanly-separated layers. Each layer is independently
testable against `io.Pipe`-based mocks; the top three layers have no
knowledge of the subprocess.

```
             ┌──────────────────────────────────────────────┐
             │  root package  (codex)                       │
             │    Client, Thread, Turn, Query, Startup      │
             │    dispatcher goroutine, approval callback   │
             └──────────────────────────────────────────────┘
                              │
                              ▼
             ┌──────────────────────────────────────────────┐
             │  internal/events                             │
             │    notification → typed ThreadEvent          │
             │    item.type     → typed ThreadItem          │
             │    delta.type    → typed ItemDelta           │
             │    approval method → typed ApprovalRequest   │
             └──────────────────────────────────────────────┘
                              │
                              ▼
             ┌──────────────────────────────────────────────┐
             │  internal/jsonrpc                            │
             │    LineWriter    (mutex-serialized stdin)    │
             │    LineReader    (2 MiB buffer)              │
             │    Demux         (classify + route frames)   │
             │    IDAllocator                               │
             └──────────────────────────────────────────────┘
                              │
                              ▼
             ┌──────────────────────────────────────────────┐
             │  internal/transport                          │
             │    AppServer     (spawn + 3-stage shutdown)  │
             │    cli_discovery, cli_version                │
             │    ringBuffer    (stderr tail for errors)    │
             └──────────────────────────────────────────────┘
```

The `types/` package is a leaf — every layer can import it. Errors are
declared there with `Is*()` helpers that see through `fmt.Errorf %w`.

## State Ownership

Codex app-server is the SDK's only persisted state boundary. The Go SDK
does not read `~/.codex/sessions` directly and does not scrape provider
files. All persisted history access goes through app-server RPCs.

Read-only history APIs:

- `Client.ListThreads(ctx)` returns the first `thread/list` page for
  backward-compatible callers.
- `Client.ListThreadsPage(ctx, opts)` exposes app-server pagination and
  filters (`limit`, `cursor`, archived, cwd, search, sorting, source, and
  provider filters).
- `Client.ReadThread(ctx, threadID, opts)` calls `thread/read` and can
  request persisted turns with `IncludeTurns`.
- `Client.GetThreadMessages(ctx, threadID, opts)` extracts user and
  assistant messages from `thread/read includeTurns`.
- Thread, turn, item, and list records keep raw JSON for schema drift.

Mutating / turn-consuming APIs:

- `StartThread`, `ResumeThread`, `ForkThread`, `ArchiveThread`, and
  thread command methods mutate app-server state.
- `Thread.Run` and `Thread.RunStreamed` call `turn/start` and consume
  model turns.
- History reads never call `thread/resume`, `thread/start`, or
  `turn/start`.

Provider files touched:

- Normal history reads touch no provider files directly.
- `WithHookCallback` writes generated `hooks.json` into an isolated
  temporary `CODEX_HOME` by default.
- `HookConfigModeUserHome` is the explicit opt-in mode that backs up and
  temporarily writes user `~/.codex/hooks.json`.

Drift guard:

- The vendored app-server schema covers `thread/read`,
  paged/filterable `thread/list`, and turn item shapes. Tests parse mock
  JSON-RPC fixtures and preserve raw fields so new app-server keys do not
  become data loss.

## The dispatcher

When `Client.Connect` succeeds, a single goroutine starts:

```
    for {
      select {
      case note := <-demux.Notifications():
          ev := events.ParseEvent(note)
          threadID := extractThreadID(ev)             // includes UnknownEvent best-effort IDs
          client.threads[threadID].deliverEvent(ev)   // when an SDK Thread is registered
          client.publishThreadEvent(threadID, ev)     // includes provider-created child threads

      case sreq := <-demux.ServerRequests():
          req := events.ParseApprovalRequest(sreq.Method, sreq.Params)
          decision := client.opts.ApprovalCallback(ctx, req)
          demux.RespondServerRequest(sreq.ID,
                                     events.EncodeApprovalDecision(decision),
                                     nil)
      }
    }
```

One dispatcher serves all threads. Each registered Thread owns a buffered
inbox (256 events). `Client.SubscribeThreadEvents` adds bounded client-wide
subscriptions for provider-created child threads that have no `Thread` handle.
A subscriber overflow, rejected notification, or unexpected app-server event
source close emits a terminal gap error and closes instead of silently
presenting an incomplete transcript. Intentional client shutdown closes the
subscription without a gap.

## The turn lock

Each Thread carries a `turnMu`. `Run` and `RunStreamed` acquire it at
`turn/start` time and release it when `turn/completed` (or
`turn/failed`) arrives. Rationale: codex queues concurrent `turn/start`
RPCs on one thread but collapses their event boundaries — client-side
serialization is mandatory to preserve the "1 message → 1 turn → 1
completion" contract.

For RunStreamed, the unlock happens inside an internal goroutine that
forwards events and releases on terminus. The caller sees a normal
channel that closes when the turn completes.

## Concurrency contract

| Call | Safe from multiple goroutines? |
|---|---|
| `Client.{StartThread, ResumeThread, ListThreads, ListThreadsPage, ReadThread, GetThreadMessages, ForkThread, ArchiveThread}` | Yes |
| `Thread.Run` / `Thread.RunStreamed` on the SAME thread | Serialized via turnMu — later calls block |
| `Thread.Run` / `Thread.RunStreamed` on DIFFERENT threads of one Client | Yes — they share the dispatcher but have independent inboxes |
| `Thread.Interrupt` | Yes |
| `Client.SubscribeThreadEvents` | Yes; each subscription is FIFO and independently bounded |
| `Client.{ListBackgroundTerminals, TerminateBackgroundTerminal, CleanBackgroundTerminals, InterruptThreadTurn}` | Yes |
| `Client.Close` | Yes (idempotent) |
| `ApprovalCallback` invocation | Serialized per Client — one request at a time |

Stdin writes are ALL serialized via one mutex inside the LineWriter. No
frame ever interleaves on the wire.

## Shutdown

`Client.Close` is a 3-stage ladder:

1. Cancel dispatcher context + close demux (unblocks in-flight Sends).
2. Close the stdin pipe (most agents exit on EOF). Wait up to 3s.
3. SIGTERM. Wait up to 2s.
4. SIGKILL.

The captured stderr tail is rolled into any `*types.ProcessError`
returned from the final Wait.

## Forward compatibility

Every type hierarchy has an `Unknown*` fallback:

| Hierarchy | Unknown type |
|---|---|
| `types.ThreadEvent` | `*types.UnknownEvent{Method, Params}` |
| `types.ThreadItem` | `*types.UnknownItem{Type, Raw}` |
| `types.ItemDelta` | `*types.UnknownDelta{Type, Raw}` |
| `types.ApprovalRequest` | `*types.UnknownApprovalRequest{Method, Params}` |

When codex introduces a new event/item/delta subtype in a future CLI
version, the SDK keeps working — users can type-switch on the Unknown
variant to get the raw payload. Unknown notifications also carry
best-effort thread/turn/item IDs when those fields are present, so
thread-scoped future events are still routed to the owning Thread.

The vendored app-server schema is part of CI coverage. `make
check-schema-drift` regenerates schema from the installed `codex` binary
and fails when a new notification method lacks typed support or an
intentional route/drop policy.
