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

## The dispatcher

When `Client.Connect` succeeds, a single goroutine starts:

```
    for {
      select {
      case note := <-demux.Notifications():
          ev := events.ParseEvent(note)
          threadID := extractThreadID(ev)
          client.threads[threadID].deliverEvent(ev)   // drop if unknown

      case sreq := <-demux.ServerRequests():
          req := events.ParseApprovalRequest(sreq.Method, sreq.Params)
          decision := client.opts.ApprovalCallback(ctx, req)
          demux.RespondServerRequest(sreq.ID,
                                     events.EncodeApprovalDecision(decision),
                                     nil)
      }
    }
```

One dispatcher across all threads. Each Thread owns a buffered inbox
(256 events) so a slow consumer can't stall the dispatcher.

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
| `Client.{StartThread, ResumeThread, ListThreads, ForkThread, ArchiveThread}` | Yes |
| `Thread.Run` / `Thread.RunStreamed` on the SAME thread | Serialized via turnMu — later calls block |
| `Thread.Run` / `Thread.RunStreamed` on DIFFERENT threads of one Client | Yes — they share the dispatcher but have independent inboxes |
| `Thread.Interrupt` | Yes |
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
variant to get the raw payload.
