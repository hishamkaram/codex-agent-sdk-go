# Wire Protocol Reference

The SDK speaks JSON-RPC 2.0 to `codex app-server` over stdio. This doc
lists the methods the SDK knows about and documents observed wire
quirks.

## Framing

- Line-delimited JSON, LF terminator.
- `"jsonrpc":"2.0"` field is OMITTED on both directions. The server
  tolerates its absence; omitting saves bytes and matches the upstream
  Python SDK.
- Client→server reader buffer minimum 2 MiB. User input cap is 1 MiB;
  envelope overhead pushes some notifications past that.

## Three frame classes

Every inbound line classifies into one of three shapes:

| Shape | Meaning | Example |
|---|---|---|
| `{id, method, params}` | server-initiated request — client MUST respond with matching id | `{"id":99,"method":"item/commandExecution/requestApproval","params":{...}}` |
| `{method, params}` (no `id`) | notification | `{"method":"turn/started","params":{...}}` |
| `{id, result}` or `{id, error}` | response to a client-initiated request | `{"id":1,"result":{"thread":{"id":"..."}}}` |

The demux dispatches each class to the correct channel. See
`internal/jsonrpc/demux.go`.

## Client-initiated methods

The SDK sends these requests. Response shapes are best-effort —
field names preferred in this order: flat > nested.

| Method | Params | Response carries | SDK caller |
|---|---|---|---|
| `initialize` | `{clientInfo:{name,version,title?},capabilities:{experimentalApi}}` | `{userAgent,codexHome,platformFamily,platformOs}` | `Client.Connect` |
| `initialized` (notification) | none | — | `Client.Connect` |
| `thread/start` | `{cwd?,model?,sandbox?,approvalPolicy?}` | `{thread:{id,…}}` | `Client.StartThread` |
| `thread/resume` | `{threadId,cwd?}` | `{thread:{id,…}}` | `Client.ResumeThread` |
| `thread/list` | `{}` | `{threads:[…]}` | `Client.ListThreads` |
| `thread/fork` | `{sourceThreadId,…}` | `{thread:{id,…}}` | `Client.ForkThread` |
| `thread/archive` | `{threadId}` | `{}` | `Client.ArchiveThread` |
| `turn/start` | `{threadId,input:[{type:"text"|"localImage",…}],outputSchema?}` | `{turn:{id,…}}` | `Thread.RunStreamed` |
| `turn/interrupt` | `{threadId,turnId}` | `{}` | `Thread.Interrupt` |

## Server-initiated notifications (→ ThreadEvent)

The SDK recognizes these method names. Unrecognized methods return
`*types.UnknownEvent` — the raw params are preserved.

| Method | Go type |
|---|---|
| `thread/started` | `*types.ThreadStarted` |
| `turn/started` | `*types.TurnStarted` |
| `turn/completed` | `*types.TurnCompleted` |
| `turn/failed` | `*types.TurnFailed` |
| `item/started` | `*types.ItemStarted` |
| `item/updated` | `*types.ItemUpdated` |
| `item/agentMessage/delta` | `*types.ItemUpdated` (normalized, wrapping `AgentMessageDelta`) |
| `item/completed` | `*types.ItemCompleted` |
| `thread/tokenUsage/updated` | `*types.TokenUsageUpdated` |
| `compaction_event` | `*types.CompactionEvent` |
| `error` | `*types.ErrorEvent` |

### Observed-but-not-typed (UnknownEvent fallback in v0.1.0)

Seen in the captured spike transcript; routed to `UnknownEvent`. Add
typed handling in a future version if needed:

- `mcpServer/startupStatus/updated`
- `thread/status/changed`
- `account/rateLimits/updated`
- `configWarning`
- `thread/archived`
- `serverRequest/resolved`

## Server-initiated requests (approvals)

These are REQUESTS (with `id`), not notifications. The client MUST
respond. The SDK dispatches to `ApprovalCallback` and sends the encoded
decision back.

| Method | Go type |
|---|---|
| `item/commandExecution/requestApproval` | `*types.CommandExecutionApprovalRequest` |
| `item/fileChange/requestApproval` | `*types.FileChangeApprovalRequest` |
| `item/permissions/requestApproval` | `*types.PermissionsApprovalRequest` |
| `mcpServer/elicitation/request` | `*types.ElicitationRequest` |

Decision wire shape: `{decision: "accept"|"acceptForSession"|"decline"|"cancel", reason?}`.

## Wire quirks (derived from real transcripts)

### Flat vs nested ID shapes

Some notifications use nested objects, others use flat keys. The parser
tries flat first, falls back to nested.

```json
// nested (thread/started real form)
{"method":"thread/started","params":{"thread":{"id":"019d…"}}}

// flat (alternative; used by some other methods)
{"method":"turn/started","params":{"threadId":"T1","turnId":"U1"}}
```

### Item discriminators are camelCase, not snake_case

Early design used `agent_message`, `user_message`, `command_execution`
etc. The real wire uses camelCase: `agentMessage`, `userMessage`,
`commandExecution`, `fileChange`, `mcpToolCall`, `webSearch`,
`memoryRead`, `memoryWrite`, `plan`, `reasoning`, `systemError`. The
types package matches this ground truth.

### Field names are camelCase too

Same pattern in individual item fields — `aggregatedOutput` not
`aggregated_output`, `exitCode` not `exit_code`, `durationMs` not
`duration_ms`. TokenUsage uses `inputTokens`, `outputTokens`,
`cachedInputTokens`, `reasoningOutputTokens`, `totalTokens`.

### `AgentMessage.Text` not `AgentMessage.Content`

Wire field is `text`. The struct exposes `Text string` with JSON tag
`"text"`. `AgentMessage` additionally carries `ID`, `Phase`, and
`MemoryCitation`.

### `UserMessage.Content` is an ARRAY of parts

```json
{"type":"userMessage","content":[{"type":"text","text":"Reply with exactly: OK"}]}
```

The Go type is `Content []UserMessagePart` — not a single string.

### `turn/completed` nests status inside `turn`

```json
{"threadId":"T1","turn":{"id":"U1","status":"completed","durationMs":2194}}
```

Status values observed: `"completed"` (success) and `"failed"`. The
parser tolerates BOTH the nested shape and a flat fallback.

### `turn/completed` does NOT carry usage

Usage flows as a separate stream via `thread/tokenUsage/updated` with
shape:

```json
{"threadId":"T1","turnId":"U1",
 "tokenUsage":{"total":{"totalTokens":12632,"inputTokens":12615,
                        "cachedInputTokens":4480,"outputTokens":17,
                        "reasoningOutputTokens":10},
               "last":{"…per-turn-slice…"},
               "modelContextWindow":258400}}
```

The SDK surfaces `total` (running thread total) on
`TokenUsageUpdated.Usage`. `Thread.Run` tracks the latest snapshot and
assigns it to `Turn.Usage` when the turn terminates.

### `item/agentMessage/delta` is a per-item-type delta method

Initial design assumed streaming text came via generic `item/updated`
with `{delta:{type:"agent_message_delta",text_chunk:"…"}}`. The real
wire uses a dedicated method with a FLAT string delta:

```json
{"method":"item/agentMessage/delta",
 "params":{"threadId":"T1","turnId":"U1","itemId":"msg_…","delta":"OK"}}
```

The parser normalizes this into `*types.ItemUpdated{Delta: *AgentMessageDelta{TextChunk: "OK"}}`
so callers see a single event shape.

Analogous per-item-type delta methods for `reasoning` and
`commandExecution` output likely exist but are NOT in the captured
spike transcript; they route to `UnknownEvent` pending ground truth.

### `reasoning.summary` / `reasoning.content` are arrays

Both fields are JSON arrays on the wire (often empty during streaming,
populated when complete). The SDK stores them as
`[]json.RawMessage` to preserve shape across CLI versions.

### Concurrent `turn/start` on one thread

The server QUEUES concurrent `turn/start` calls on the same thread but
collapses their event boundaries. The SDK serializes via a per-thread
`turnMu`; callers that make concurrent Run calls block, they don't
race.

## References

- Captured transcript: `internal/events/testdata/spike-transcript.jsonl`
  (523 lines, real `codex app-server` v0.121.0)
- `internal/jsonrpc/types.go` — envelope types
- `internal/events/parser.go` — method → event dispatch
- `internal/events/items.go` — item.type → ThreadItem dispatch
