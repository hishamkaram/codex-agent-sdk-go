# Architecture

The root package, `codex`, exposes `Client`, `Thread`, `Query` and public runtime
probes. `Client` coordinates the internal packages below. The `types` package
holds public options, events and errors and imports no SDK internals.

- [transport](../internal/transport) owns CLI discovery, process lifecycle and
  stdio. It uses [jsonrpc](../internal/jsonrpc) for framing and request routing.
- [events](../internal/events) parses JSON-RPC notifications and provider
  requests into [types](../types) and encodes callback decisions.
- [hookbridge](../internal/hookbridge) implements the socket protocol shared
  by the client and the [hook shim](../cmd/codex-sdk-hook-shim).
- [log](../internal/log) wraps injected zap loggers for SDK packages.

The root client composes these internals. Internal packages depend on public
values in types and on the lower-level packages above as needed. They never
import the root client.

## Change map

Run focused Go checks from the repository root with `GOWORK=off`. The examples
and guides in the map show the corresponding public use cases.

| Task | Owner, state and boundary | Focused checks and consumers |
| --- | --- | --- |
| Connect, close, route or subscribe | [Client](../client.go) owns the registry, connection attempt, and post-connect dispatcher. [Dispatch](../client_dispatch.go) reads ordered server messages, delivers to registered threads, and publishes [subscriptions](../client_thread_events.go) for provider-created child activity. Teardown belongs to Client. | [Checks](#client-lifecycle-and-subscriptions). See the [streaming example](../examples/streaming/main.go). |
| Start, resume, fork, run, or control a thread | [Client](../client.go) owns one [execution record](../thread_execution.go) per provider thread ID. Handles share admission and lifecycle evidence. Stream readers own bounded consumption. [Thread commands](../threads_commands.go) use the same handle, and [background terminal controls](../background_terminals.go) send exact provider IDs through the Client's RPC session. | [Checks](#threads-and-turn-controls). See [commands](commands.md), the [fork example](../examples/fork/main.go) and [MCP example](../examples/with_mcp/main.go). |
| Frame, correlate, parse or reply | [LineWriter](../internal/jsonrpc/framing.go) owns frame serialization. [Demux](../internal/jsonrpc/demux.go) owns pending requests by ID. [Approval parsing and encoding](../internal/events/approvals.go) are protocol translations. The ordered dispatcher captures file-approval context. One [approval worker](../client_approval_worker.go) runs callbacks and replies on the matching request ID. | [Checks](#framing-and-approvals). See [wire protocol](wire-protocol.md), [approvals](approvals.md) and the [approval example](../examples/with_approvals/main.go). |
| Change a public event, option, usage counter, error, or history record | [types](../types) defines the public values. [The parser](../internal/events/parser.go) maps notifications and preserves unknown shapes. [Usage parsing](../internal/events/parser_lifecycle.go) reads provider snapshots. Thread.Run assembles the buffered result ([threads.go](../threads.go)), and [history](../thread_history.go) reads persisted records by RPC. | [Checks](#public-types-and-history). See the [wire protocol guide](wire-protocol.md) and [streaming example](../examples/streaming/main.go). |
| Change executable discovery, spawn, environment, or optional capability detection | [transport](../internal/transport) owns executable resolution, child environment, pipes and reaping. The root's [runtime feature probe](../runtime_features.go) validates provider schemas. The [runtime control probe](../runtime_controls.go) checks CLI help, optionally checks the experimental schema, and applies supplied provider requirements. A CLI version alone cannot prove support. | [Checks](#transport-and-capability-probes). See [runtime prerequisites](../README.md#prerequisites) and [commands](commands.md). |
| Change runtime hook configuration or execution | [Client hook setup](../client_hookbridge.go) owns generated config, backups, and cleanup. The [listener](../internal/hookbridge/listener.go) owns its socket and serves the shim. Close stops the app-server before tearing down the listener and removing or restoring the config. | [Checks](#runtime-hooks). See [hooks](hooks.md) and the [hooks example](../examples/with_hooks/main.go). |
| Validate a behavior or update its guidance | The owning package keeps deterministic regression tests. Root-level [fake peer tests](../client_flow_test.go) exercise public flows and wire requests together. [Integration tests](../tests) cover real CLI boundaries. The consumer-facing example and guide follow the API or wire behavior. | Start with the owning row's focused tests, use `make examples` for changed examples, and finish with `make verify`. See [contributing](../CONTRIBUTING.md) for optional schema and live checks. |

### Focused checks

#### Client lifecycle and subscriptions

```bash
go test -race -short . -run 'Test(Client_.*Connect|Client_Close|SubscribeThreadEvents)'
```

#### Threads and turn controls

```bash
go test -race -short . -run 'Test(Client_StartThreadAndRun|Thread_Interrupt|Thread_ActiveTurnID|BuildThreadStartParams|TerminateBackgroundTerminalAndInterruptThreadTurn)'
```

#### Framing and approvals

```bash
go test -race -short ./internal/jsonrpc ./internal/events
go test -race -short . -run 'TestClient_ApprovalCallback|TestFileApprovalOrderedDispatch'
```

#### Public types and history

```bash
go test -race -short ./types ./internal/events
go test -race -short . -run 'TestClient_ThreadRun|TestReadThreadAndMessages|TestListThreadsPage'
```

#### Transport and capability probes

```bash
go test -race -short ./internal/transport
go test -race -short . -run 'TestDiscoverRuntime(Features|Controls)'
```

#### Runtime hooks

```bash
go test -race -short ./internal/hookbridge ./cmd/codex-sdk-hook-shim
go test -race -short . -run 'TestSetupHookBridge|TestInstallHooksJSON|TestRestoreUserHooksJSON'
```

## Boundaries and known gaps

The [parity tracker](https://github.com/hishamkaram/codex-agent-sdk-go/issues/57)
owns the behavior backlog. Preserve these distinctions when changing its
affected paths.

- **Cancellation.** The Connect context bounds initialization. The Client
  owns subprocess lifetime after the handshake. Each turn context bounds
  local consumption and can trigger targeted interruption of an owned turn.
  See [turn execution](#turn-execution) for ownership and reuse.
- **Approvals.** Keep the ordered path from file notifications to callback and
  preserve correlation to the original request ID. The shared encoder selects
  the wire response from the request method and validates the callback decision.
- **Thread options.** The [thread config builder](../thread_config.go)
  combines defaults and per-call options into a fresh request. Start and fork
  share MCP and workspace-write configuration. Resume uses only its supported
  overrides so the server can restore saved workspace-write policy. The
  [policy readback check](../workspace_write.go) verifies requested workspace
  options before the Client registers a new handle. Fork identifies the parent
  with `threadId` and registers the child under its own ID. See
  [thread configuration](thread-options.md).
- **Usage.** Streaming usage preserves provider snapshots. The ordered
  [usage ledger](../thread_usage.go) measures buffered usage from matching
  terminal evidence and tracks provider cache reads and writes independently.
- **Launch options.** Transport resolves the child environment once for each
  connection and its CLI probes. Overlay is the default, and replacement uses
  a caller allowlist. The Client owns connection-scoped proof of experimental
  turn options. See [launch options](launch-options.md).

## Choosing a package

Use the smallest existing owner with the state and callers needed for the
change. The [public client and dispatch split](https://github.com/hishamkaram/codex-agent-sdk-go/pull/32)
already leaves framing, parsing, and subprocess lifecycle in focused internals.
Moving Client dispatch alone would expose the private thread registry and
approval state. Moving thread execution alone would expose the execution registry and
the Client's private RPC session. Keep both paths in the root
package and make the next shared approval encoder in events and the next
thread config builder beside the thread RPC calls. Consider package extraction
in a focused change only when it can remove a demonstrated dependency or
state boundary problem without changing public imports or copying state.

## Persisted history and hook configuration

Codex app-server owns persisted thread and turn history. All SDK history
access goes through app-server RPCs. Local thread handles route activity for
connected callers.

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

The app-server preserves input order from file lifecycle notifications
through approval-context capture. Client dispatch records turn lifecycle and
usage before publishing to subscribers. One bounded FIFO approval worker
invokes callbacks independently of that dispatcher. A second bounded lane
handles non-accepting overflow replies. Shutdown cancels and joins both workers
within the caller's `Close` context.

Active operation streams have a 256-event queue with matching terminal
evidence retained separately. Client-wide subscriptions have independent
buffers. An overflow, undecodable frame, failed thread notification parse, or
unexpected source close yields a terminal gap for affected subscribers.
Intentional client shutdown closes subscriptions normally.

## Turn execution

A `Client` serializes runs, inline reviews, and compaction by provider thread
ID across handles. Calls wait cancellably behind SDK-owned work. Before the
serialized writer sends a reserved request, it rechecks observed provider
activity and lifecycle messages received for that thread. Queued activity
rejects the write until the dispatcher applies it. Valid UUID spellings
share one execution record without changing the caller's wire ID. Canceling a
run closes its local stream. The SDK asks the provider to interrupt the exact
turn after correlating the reply with a fresh root start on a thread created
by this Client. The same server reply can describe work steered onto an
existing turn, so a reply alone supplies no ownership proof. A transport or
decoding gap can invalidate ownership. The SDK logs unconfirmed cleanup and
keeps admission blocked until it can correlate completion. Ordered idle status
can resolve an external-only fence. A fresh idle resume reply that names the
same thread can also resolve that fence after the dispatcher applies earlier
wire messages. Newer activity preserves the fence. A canceled SDK operation
still requires matching terminal evidence.

The five-second reconciliation window bounds the cleanup RPC and the next
call's admission wait. A matching terminal event releases the reservation.
Until then, ambiguous or unconfirmed work can cause
`ErrThreadExecutionUnconfirmed`. An interrupt acknowledgement is receipt
evidence. The matching terminal event is termination evidence. Use a
client-wide subscription to observe provider completion after local
cancellation. Explicit `Thread.Interrupt` is available when the caller knows
the active turn. A failed partial stdin frame closes the connection.

## Concurrency contract

| Call | Safe from multiple goroutines? |
|---|---|
| `Client.{StartThread, ResumeThread, ListThreads, ListThreadsPage, ReadThread, GetThreadMessages, ForkThread, ArchiveThread}` | Yes |
| `Thread.Run` / `Thread.RunStreamed` on the SAME thread | Serialized by provider thread ID across handles |
| `Thread.Run` / `Thread.RunStreamed` on DIFFERENT threads of one Client | Yes — they share the dispatcher but have independent operation streams |
| `Thread.Interrupt` | Yes |
| `Client.SubscribeThreadEvents` | Yes; each subscription is FIFO and independently bounded |
| `Client.{ListBackgroundTerminals, TerminateBackgroundTerminal, CleanBackgroundTerminals, InterruptThreadTurn}` | Yes |
| `Client.Close` | Yes (idempotent) |
| `ApprovalCallback` invocation | Serialized per Client — one request at a time |

LineWriter owns one cancellable slot for each complete stdin frame, including replies
to server requests.


## Shutdown

`Client.Close` cancels in-flight connection or dispatch work, closes the demux,
and marks handles and subscriptions closed. `AppServer.Close` signals EOF on
stdin and waits up to three seconds. It then sends `os.Interrupt` and waits up
to two seconds before killing and reaping the process. Cancellation shortens
the grace waits. The transport bounds stderr draining and includes the captured
tail in `ProcessError` when it reports an unexpected process exit. Hook resources
are torn down after the transport stops.

## Forward compatibility

These hierarchies preserve unrecognized payloads through `Unknown*` types.

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

Unit tests check the vendored schema against parser and route coverage.
Run `make check-schema-drift` to check the installed Codex CLI against that
coverage. Use independent request and response schemas for wire contract tests.
