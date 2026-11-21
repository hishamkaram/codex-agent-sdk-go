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
| Start, resume, fork, run, or control a thread | [Thread](../threads.go) owns active turn identity, per-handle turn serialization, and bounded event delivery. Client registers handles by provider thread ID. [Thread commands](../threads_commands.go) use the same handle, and [background terminal controls](../background_terminals.go) send exact provider IDs through the Client's RPC session. | [Checks](#threads-and-turn-controls). See [commands](commands.md), the [fork example](../examples/fork/main.go) and [MCP example](../examples/with_mcp/main.go). |
| Frame, correlate, parse or reply | [LineWriter](../internal/jsonrpc/framing.go) owns frame serialization. [Demux](../internal/jsonrpc/demux.go) owns pending requests by ID. [Approval parsing and encoding](../internal/events/approvals.go) are protocol translations. The root dispatcher owns callback invocation, ordered file-approval context, and sending the reply on the request ID. | [Checks](#framing-and-approvals). See [wire protocol](wire-protocol.md), [approvals](approvals.md) and the [approval example](../examples/with_approvals/main.go). |
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

- **Cancellation.** Connect's context bounds connection setup. The persistent
  Client owns the dispatcher and subprocess after a successful handshake.
  Canceling an active `Run` or `RunStreamed` currently stops local consumption
  and releases `turnMu` without requesting a provider interrupt. Explicit
  interruption sends `turn/interrupt` with the thread ID and turn ID. [#45](https://github.com/hishamkaram/codex-agent-sdk-go/issues/45)
  tracks targeted cancellation and same-thread reuse after cancellation.
- **Approvals.** Keep the ordered path from file notifications to callback and
  preserve correlation to the original request ID. The shared encoder selects
  the wire response from the request method and validates the callback decision.
- **Thread options.** Start and fork share a defaults-and-overrides builder
  that selects per-thread or default MCP config. Resume builds its own request
  today. [#51](https://github.com/hishamkaram/codex-agent-sdk-go/issues/51)
  owns shared thread-scoped config assembly, preserving existing MCP config and
  caller-owned values. [#52](https://github.com/hishamkaram/codex-agent-sdk-go/issues/52)
  and [#53](https://github.com/hishamkaram/codex-agent-sdk-go/issues/53)
  build on it. Keep overrides in thread RPC parameters so other threads and
  the persisted session config retain their settings. Fork sends the parent ID
  as `threadId` and registers the returned child under its own ID.
- **Usage.** The parser preserves `LastUsage` and `TotalUsage`. The streaming
  `Usage` field keeps the cumulative snapshot when present. Buffered `Run`
  prefers explicit completion usage and currently falls back to the streaming
  `Usage` field. [#47](https://github.com/hishamkaram/codex-agent-sdk-go/issues/47)
  tracks adding cache-write tokens and correcting buffered per-turn accounting.
- **Environment.** Transport overlays the supplied environment on inherited
  variables. [#46](https://github.com/hishamkaram/codex-agent-sdk-go/issues/46)
  tracks exact replacement as a separate capability while preserving overlay
  defaults.

## Choosing a package

Use the smallest existing owner with the state and callers needed for the
change. The [public client and dispatch split](https://github.com/hishamkaram/codex-agent-sdk-go/pull/32)
already leaves framing, parsing, and subprocess lifecycle in focused internals.
Moving Client dispatch alone would expose the private thread registry and
approval state. Moving Thread execution alone would expose turnMu, inbox
ownership, and the Client's private RPC session. Keep both paths in the root
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

AppServer constructs an ordered server-message channel so file lifecycle
notifications can update approval context before the related request runs.
Client dispatch parses notifications, updates the registered thread's state,
then delivers to client-wide subscribers. Server requests run serially on
the dispatcher. Approval callbacks must honor the dispatcher context and
avoid waiting on the same dispatcher.

Each registered thread has a 256-event inbox. A full inbox logs a warning and
drops the incoming event. Client-wide subscriptions have independent buffers.
An overflow, undecodable frame, failed thread notification parse, or unexpected
event-source close yields a terminal gap for affected subscribers. Intentional
client shutdown closes subscriptions without a gap.

## The turn lock

Each `Thread` handle carries a `turnMu`. `Run` calls `RunStreamed`, which holds
the lock from `turn/start` through the stream worker's exit. The worker normally exits
after a matching terminal event. It also exits on context cancellation or
thread shutdown, closing the result channel. The provider can still be running
after local cancellation as described in the known gaps above.

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

One mutex inside LineWriter serializes all stdin frames, including replies
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
