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
| `initialize` | `{clientInfo:{name,version,title?},capabilities:{experimentalApi,optOutNotificationMethods?}}` | `{userAgent,codexHome,platformFamily,platformOs}` | `Client.Connect` |
| `initialized` (notification) | none | — | `Client.Connect` |
| `thread/start` | `{cwd?,model?,sandbox?,approvalPolicy?}` | `{thread:{id,…}}` | `Client.StartThread` |
| `thread/resume` | `{threadId,cwd?}` | `{thread:{id,…}}` | `Client.ResumeThread` |
| `thread/list` | `{}` | `{threads:[…]}` | `Client.ListThreads` |
| `thread/fork` | `{sourceThreadId,…}` | `{thread:{id,…}}` | `Client.ForkThread` |
| `thread/archive` | `{threadId}` | `{}` | `Client.ArchiveThread` |
| `turn/start` | `{threadId,input:[{type:"text"|"localImage",…}],outputSchema?,collaborationMode?}` | `{turn:{id,…}}` | `Thread.RunStreamed` |
| `turn/interrupt` | `{threadId,turnId}` | `{}` | `Thread.Interrupt` |

## Server-initiated notifications (→ ThreadEvent)

The SDK recognizes these method names. Unrecognized methods return
`*types.UnknownEvent` with raw params preserved and best-effort
`thread_id`/`turn_id`/`item_id` extraction so thread-scoped unknowns can
still be routed instead of silently dropped.

| Method | Go type |
|---|---|
| `thread/started` | `*types.ThreadStarted` |
| `thread/archived` | `*types.ThreadArchived` |
| `thread/unarchived` | `*types.ThreadUnarchived` |
| `thread/closed` | `*types.ThreadClosed` |
| `thread/name/updated` | `*types.ThreadNameUpdated` |
| `thread/status/changed` | `*types.ThreadStatusChanged` |
| `thread/compacted`, `compaction_event` | `*types.ContextCompacted` |
| `thread/tokenUsage/updated` | `*types.TokenUsageUpdated` |
| `thread/goal/updated` | `*types.ThreadGoalUpdated` |
| `thread/goal/cleared` | `*types.ThreadGoalCleared` |
| `turn/started` | `*types.TurnStarted` |
| `turn/completed` | `*types.TurnCompleted` |
| `turn/failed` | `*types.TurnFailed` |
| `turn/diff/updated` | `*types.TurnDiffUpdated` |
| `turn/plan/updated` | `*types.TurnPlanUpdated` |
| `item/started` | `*types.ItemStarted` |
| `item/updated` | `*types.ItemUpdated` |
| `item/agentMessage/delta` | `*types.ItemUpdated` (normalized, wrapping `AgentMessageDelta`) |
| `item/commandExecution/outputDelta` | `*types.ItemUpdated` (normalized, wrapping `CommandOutputDelta`) |
| `item/fileChange/outputDelta` | `*types.ItemUpdated` (normalized, wrapping `FileChangeOutputDelta`) |
| `item/fileChange/patchUpdated` | `*types.FileChangePatchUpdated` |
| `item/plan/delta` | `*types.ItemUpdated` (normalized, wrapping `PlanDelta`) |
| `item/reasoning/textDelta` | `*types.ItemUpdated` (normalized, wrapping `ReasoningTextDelta`) |
| `item/reasoning/summaryTextDelta` | `*types.ItemUpdated` (normalized, wrapping `ReasoningSummaryTextDelta`) |
| `item/reasoning/summaryPartAdded` | `*types.ItemUpdated` (normalized, wrapping `ReasoningSummaryPartAdded`) |
| `item/mcpToolCall/progress` | `*types.ItemUpdated` (normalized, wrapping `MCPToolCallProgress`) |
| `item/commandExecution/terminalInteraction` | `*types.ItemUpdated` (normalized, wrapping `TerminalInteraction`) |
| `item/autoApprovalReview/started` | `*types.ItemGuardianApprovalReviewStarted` |
| `item/autoApprovalReview/completed` | `*types.ItemGuardianApprovalReviewCompleted` |
| `item/completed` | `*types.ItemCompleted` |
| `thread/realtime/started` | `*types.ThreadRealtimeStarted` |
| `thread/realtime/closed` | `*types.ThreadRealtimeClosed` |
| `thread/realtime/error` | `*types.ThreadRealtimeError` |
| `thread/realtime/itemAdded` | `*types.ThreadRealtimeItemAdded` |
| `thread/realtime/outputAudio/delta` | `*types.ThreadRealtimeOutputAudioDelta` |
| `thread/realtime/sdp` | `*types.ThreadRealtimeSdp` |
| `thread/realtime/transcript/delta` | `*types.ThreadRealtimeTranscriptDelta` |
| `thread/realtime/transcript/done` | `*types.ThreadRealtimeTranscriptDone` |
| `mcpServer/startupStatus/updated` | `*types.MCPServerStartupStatusUpdated` |
| `mcpServer/oauthLogin/completed` | `*types.MCPServerOAuthLoginCompleted` |
| `account/login/completed` | `*types.AccountLoginCompleted` |
| `account/rateLimits/updated` | `*types.AccountRateLimitsUpdated` |
| `account/updated` | `*types.AccountUpdated` |
| `model/rerouted` | `*types.ModelRerouted` |
| `model/verification` | `*types.ModelVerification` |
| `command/exec/outputDelta` | `*types.CommandExecOutputDelta` |
| `process/outputDelta` | `*types.ProcessOutputDelta` |
| `process/exited` | `*types.ProcessExited` |
| `remoteControl/status/changed` | `*types.RemoteControlStatusChanged` |
| `externalAgentConfig/import/completed` | `*types.ExternalAgentConfigImportCompleted` |
| `configWarning` | `*types.ConfigWarning` |
| `warning` | `*types.Warning` |
| `guardianWarning` | `*types.GuardianWarning` |
| `deprecationNotice` | `*types.DeprecationNotice` |
| `fs/changed` | `*types.FsChanged` |
| `skills/changed` | `*types.SkillsChanged` |
| `app/list/updated` | `*types.AppListUpdated` |
| `serverRequest/resolved` | `*types.ServerRequestResolved` |
| `windows/worldWritableWarning` | `*types.WindowsWorldWritableWarning` |
| `windowsSandbox/setupCompleted` | `*types.WindowsSandboxSetupCompleted` |
| `fuzzyFileSearch/sessionUpdated` | `*types.FuzzyFileSearchSessionUpdated` |
| `fuzzyFileSearch/sessionCompleted` | `*types.FuzzyFileSearchSessionCompleted` |
| `hook/started` | `*types.HookStarted` |
| `hook/completed` | `*types.HookCompleted` |
| `error` | `*types.ErrorEvent` |

Parser coverage is guarded by the vendored schema drift tests. After a
Codex upgrade, run `make check-schema-drift`; new thread-scoped methods
must either gain typed parser support or an intentional route/drop policy.

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
| `item/tool/requestUserInput` | `*types.ToolRequestUserInputRequest` |

Decision wire shape: `{decision: "accept"|"acceptForSession"|"decline"|"cancel", reason?}`.
`item/tool/requestUserInput` uses `{answers:{[questionId]:{answers:[...]}}}` instead.

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

The SDK keeps `total` (running thread total) on `TokenUsageUpdated.Usage`
for compatibility, exposes the per-turn `last` snapshot on
`TokenUsageUpdated.LastUsage`, and preserves explicit cumulative `total` on
`TokenUsageUpdated.TotalUsage`. `modelContextWindow` is surfaced on
`TokenUsageUpdated.ModelContextWindow`. `Thread.Run` tracks the latest
snapshot and assigns it to `Turn.Usage` when the turn terminates.

Important: `total` is a cumulative per-thread counter, not a
current-context occupancy metric. It is safe for lifetime cost/token
accounting, but callers MUST NOT derive context-window percentage from
`total / modelContextWindow`.

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

Analogous per-item-type delta methods for command output, file-change
output, plans, reasoning text, reasoning summary text, reasoning summary
parts, MCP progress, and terminal interaction are normalized into the
same `*types.ItemUpdated` event shape.

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
- Vendored schema: `internal/events/testdata/schema/codex_app_server_protocol.v2.schemas.json`
  (regenerated from `codex app-server generate-json-schema` with Codex
  0.130.0 on 2026-05-13)
- `internal/jsonrpc/types.go` — envelope types
- `internal/events/parser.go` — method → event dispatch
- `internal/events/items.go` — item.type → ThreadItem dispatch
